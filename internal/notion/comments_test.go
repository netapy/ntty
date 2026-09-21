package notion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func TestCommentsRequestPaginationAndAuthors(t *testing.T) {
	c := NewClient()
	c.interval = 0
	calls := 0
	next := "cursor+with/slash?and=space &"
	c.run = func(_ context.Context, args []string, body []byte) ([]byte, error) {
		calls++
		path, err := url.Parse(args[1])
		if err != nil {
			return nil, err
		}
		query := path.Query()
		if args[3] != "GET" || path.Path != "v1/comments" || body != nil || query.Get("block_id") != "page-id" || query.Get("page_size") != "50" {
			return nil, fmt.Errorf("wrong list request: %v %s", args, body)
		}
		if calls == 1 {
			if query.Has("start_cursor") {
				t.Error("first request included a cursor")
			}
			cursor, _ := json.Marshal(next)
			return []byte(`{"object":"list","results":[{"object":"comment","id":"c1","discussion_id":"thread","created_time":"2026-09-20T10:00:00Z","last_edited_time":"2026-09-20T10:01:00Z","created_by":{"id":"author-id"},"display_name":{"resolved_name":"Alex"},"rich_text":[{"plain_text":"Hello "},{"plain_text":"@Sam"},{"text":{"content":" café"}}],"attachments":[{}],"original_content_deleted":true}],"has_more":true,"next_cursor":` + string(cursor) + `}`), nil
		}
		if query.Get("start_cursor") != next {
			t.Errorf("cursor was corrupted: %q", query.Get("start_cursor"))
		}
		return []byte(`{"object":"list","results":[{"object":"comment","id":"c2","discussion_id":"thread","created_by":{"id":"author-only"},"display_name":{"resolved_name":null},"rich_text":[{"plain_text":"Reply"}]}],"has_more":false,"next_cursor":"ignored","request_status":{"type":"incomplete"}}`), nil
	}
	first, err := c.ListComments(context.Background(), "page-id", "")
	if err != nil || len(first.Comments) != 1 || first.Cursor != next || calls != 1 {
		t.Fatalf("first page must not auto-paginate: %+v %v (%d calls)", first, err, calls)
	}
	comment := first.Comments[0]
	if comment.Author != "Alex" || comment.AuthorID != "author-id" || comment.Text != "Hello @Sam café" || comment.Attachments != 1 || !comment.OriginalContentDeleted || comment.Edited != "2026-09-20T10:01:00Z" {
		t.Fatalf("lost comment metadata: %+v", comment)
	}
	second, err := c.ListComments(context.Background(), "page-id", first.Cursor)
	if err != nil || second.Cursor != "" || !second.Incomplete || len(second.Comments) != 1 || second.Comments[0].AuthorID != "author-only" || calls != 2 {
		t.Fatalf("next page: %+v %v (%d calls)", second, err, calls)
	}
}

func TestCommentsRejectBrokenPagination(t *testing.T) {
	for _, response := range []string{
		`{"object":"page","results":[]}`,
		`{"object":"list","results":[],"has_more":true,"next_cursor":null}`,
		`{"object":"list","results":[],"has_more":true,"next_cursor":"same"}`,
		`{"object":"list","results":[{"object":"comment","id":"missing-thread"}]}`,
	} {
		t.Run(response, func(t *testing.T) {
			c := NewClient()
			c.interval = 0
			c.run = func(context.Context, []string, []byte) ([]byte, error) { return []byte(response), nil }
			if _, err := c.ListComments(context.Background(), "page", "same"); err == nil {
				t.Fatal("invalid response was accepted as a complete comments list")
			}
		})
	}
}

func TestCreateCommentPageAndReplyShapes(t *testing.T) {
	for _, target := range []struct{ page, discussion string }{{page: "page"}, {discussion: "thread"}} {
		t.Run(target.page+target.discussion, func(t *testing.T) {
			c := NewClient()
			c.interval = 0
			text := "  **Café** [link](https://example.com)\nSecond line.  "
			calls := 0
			c.run = func(_ context.Context, args []string, body []byte) ([]byte, error) {
				calls++
				if args[1] != "v1/comments" || args[3] != "POST" {
					t.Errorf("unexpected create route: %v", args)
				}
				var req map[string]any
				if err := json.Unmarshal(body, &req); err != nil {
					return nil, err
				}
				if len(req) != 2 || req["markdown"] != text {
					t.Errorf("comment text/shape changed: %s", body)
				}
				if target.page != "" {
					parent, ok := req["parent"].(map[string]any)
					if !ok || len(parent) != 1 || parent["page_id"] != target.page || req["discussion_id"] != nil {
						t.Errorf("bad page target: %s", body)
					}
				} else if req["discussion_id"] != target.discussion || req["parent"] != nil {
					t.Errorf("bad discussion target: %s", body)
				}
				// A write-only integration gets a partial success, not an error.
				return []byte(`{"object":"comment","id":"sent-id"}`), nil
			}
			comment, err := c.CreateComment(context.Background(), target.page, target.discussion, text)
			if err != nil || comment.ID != "sent-id" || calls != 1 {
				t.Fatalf("partial response did not confirm send: %+v %v (%d calls)", comment, err, calls)
			}
		})
	}
}

func TestCommentValidationGateAndNoAmbiguousRetry(t *testing.T) {
	c := NewClient()
	c.interval = 0
	calls := 0
	c.run = func(context.Context, []string, []byte) ([]byte, error) {
		calls++
		return nil, errors.New("503 service_unavailable")
	}
	for _, input := range [][3]string{{"", "", "hello"}, {"page", "thread", "hello"}, {"page", "", " \n "}} {
		if _, err := c.CreateComment(context.Background(), input[0], input[1], input[2]); err == nil {
			t.Fatal("invalid target or empty comment accepted")
		}
	}
	if _, err := c.ListComments(context.Background(), "", ""); err == nil || calls != 0 {
		t.Fatal("invalid comments input reached the API")
	}
	_, err := c.CreateComment(context.Background(), "page", "", "hello")
	if err == nil || !strings.Contains(err.Error(), "503") || calls != 1 {
		t.Fatalf("ambiguous non-idempotent write must not retry: %v (%d calls)", err, calls)
	}
	// Both comments operations share the page API's cancellable gate.
	c.gate <- struct{}{}
	defer func() { <-c.gate }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.ListComments(ctx, "page", ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("list bypassed API gate: %v", err)
	}
	if _, err := c.CreateComment(ctx, "page", "", "hello"); !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("create bypassed API gate: %v (%d calls)", err, calls)
	}
}

func TestDemoCommentsPageReplyAndPagination(t *testing.T) {
	d := NewDemo(nil)
	ctx := context.Background()
	first, err := d.ListComments(ctx, "demo-welcome", "")
	if err != nil || len(first.Comments) != 2 || first.Cursor == "" {
		t.Fatalf("demo first page: %+v %v", first, err)
	}
	second, err := d.ListComments(ctx, "demo-welcome", first.Cursor)
	if err != nil || len(second.Comments) != 1 || second.Cursor != "" {
		t.Fatalf("demo last page: %+v %v", second, err)
	}
	reply, err := d.CreateComment(ctx, "", first.Comments[0].DiscussionID, "a reply")
	if err != nil || reply.DiscussionID != first.Comments[0].DiscussionID {
		t.Fatalf("demo reply: %+v %v", reply, err)
	}
	page, err := d.CreateComment(ctx, "demo-notes", "", "a new comment")
	if err != nil || page.DiscussionID == "" {
		t.Fatalf("demo page comment: %+v %v", page, err)
	}
	list, err := d.ListComments(ctx, "demo-notes", "")
	if err != nil || len(list.Comments) != 1 || list.Comments[0].ID != page.ID {
		t.Fatalf("demo write wasn't kept: %+v %v", list, err)
	}
	if _, err := d.CreateComment(ctx, "", "missing-discussion", "hello"); err == nil {
		t.Fatal("demo accepted a nonexistent discussion")
	}
	if _, err := d.ListComments(ctx, "demo-welcome", "invalid"); err == nil {
		t.Fatal("demo accepted a nonexistent cursor")
	}
}
