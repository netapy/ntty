package notion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestProtectedPageEdgeInsertionReconcilesLostResponse(t *testing.T) {
	for _, position := range []string{"start", "end"} {
		t.Run(position, func(t *testing.T) {
			base := `<database url="https://www.notion.so/db">Tasks</database>`
			want := "New section\n" + base
			if position == "end" {
				want = base + "\nNew section"
			}
			remote := base
			c := NewClient()
			c.interval = 0
			writes := 0
			c.run = func(_ context.Context, args []string, body []byte) ([]byte, error) {
				if args[3] == "GET" {
					return json.Marshal(Content{Object: "page_markdown", Markdown: remote})
				}
				writes++
				var request struct {
					Type   string `json:"type"`
					Insert struct {
						Content  string `json:"content"`
						Position struct {
							Type string `json:"type"`
						} `json:"position"`
					} `json:"insert_content"`
				}
				if err := json.Unmarshal(body, &request); err != nil {
					t.Fatal(err)
				}
				if request.Type != "insert_content" || request.Insert.Position.Type != position || strings.Contains(request.Insert.Content, "<database") {
					t.Fatalf("unsafe edge insertion: %s", body)
				}
				remote = want
				return nil, fmt.Errorf("response lost")
			}
			_, err := c.Save(context.Background(), "p", base, want)
			var attempt *SaveAttemptError
			if !errors.As(err, &attempt) {
				t.Fatalf("missing insertion checkpoint: %v", err)
			}
			result, err := c.Save(context.Background(), "p", attempt.Base.Markdown, attempt.Text)
			if err != nil || result.Markdown != want || writes != 1 {
				t.Fatalf("insertion duplicated after retry: writes=%d err=%v", writes, err)
			}
		})
	}
}

func TestNotionResponsesAndSafeWrites(t *testing.T) {
	c := NewClient()
	c.interval = 0
	c.run = func(_ context.Context, args []string, body []byte) ([]byte, error) {
		return []byte(`{"results":[{"id":"p","object":"page","parent":{"type":"workspace","workspace":true},"properties":{"Name":{"type":"title","title":[{"plain_text":"Café"}]}}},{"id":"d","object":"data_source","parent":{"type":"page_id","page_id":"parent"},"title":[{"plain_text":"Projects"}],"properties":{"Name":{"type":"title","title":{}}}}],"has_more":true,"next_cursor":"next"}`), nil
	}
	l, err := c.Search(context.Background(), "", "")
	if err != nil || len(l.Pages) != 2 || l.Pages[0].Title != "Café" || l.Pages[0].TitleProperty != "Name" || l.Pages[1].Title != "Projects" || l.Cursor != "next" {
		t.Fatalf("mixed page/schema response: %+v %v", l, err)
	}
	if l.Pages[0].ParentKind != "workspace" || l.Pages[1].ParentKind != "page_id" || l.Pages[1].ParentID != "parent" {
		t.Fatalf("page parents were not decoded: %+v", l.Pages)
	}
	remote := "original"
	writes := 0
	c.run = func(_ context.Context, args []string, body []byte) ([]byte, error) {
		if args[3] == "PATCH" {
			writes++
			var req struct {
				Type   string `json:"type"`
				Update struct {
					Content []contentUpdate `json:"content_updates"`
				} `json:"update_content"`
			}
			if err := json.Unmarshal(body, &req); err != nil {
				return nil, err
			}
			if req.Type != "update_content" || len(req.Update.Content) != 1 || req.Update.Content[0].OldStr != "original" || req.Update.Content[0].ReplaceAllMatches {
				return nil, fmt.Errorf("unsafe write: %s", body)
			}
			remote = req.Update.Content[0].NewStr
		}
		return json.Marshal(Content{Object: "page_markdown", ID: "p", Markdown: remote})
	}
	_, err = c.Save(context.Background(), "p", "outdated", "mine")
	if !errors.Is(err, ErrConflict) || writes != 0 {
		t.Fatalf("conflict must not write: %v (%d writes)", err, writes)
	}
	result, err := c.Save(context.Background(), "p", "original", "mine")
	if err != nil || result.Markdown != "mine" || writes != 1 {
		t.Fatalf("save: %+v %v", result, err)
	}
	_, err = c.Save(context.Background(), "p", "original", "mine")
	if err != nil || writes != 1 {
		t.Fatal("lost-response reconciliation must not repeat the write")
	}
	c.run = func(context.Context, []string, []byte) ([]byte, error) {
		return []byte(`{"object":"page_markdown","markdown":"partial","truncated":true}`), nil
	}
	_, err = c.Save(context.Background(), "p", "partial", "mine")
	if !errors.Is(err, ErrIncomplete) {
		t.Fatalf("truncated page must not be overwritten: %v", err)
	}
}

func TestPagePropertiesBecomeStableDisplayValues(t *testing.T) {
	var raw apiPage
	if err := json.Unmarshal([]byte(`{
		"object":"page","id":"row","last_edited_time":"2026-09-21T12:00:00Z",
		"parent":{"type":"data_source_id","data_source_id":"source"},
		"properties":{
			"Name":{"id":"title","type":"title","title":[{"plain_text":"Ship ntty"}]},
			"Status":{"id":"status","type":"status","status":{"name":"In progress"}},
			"Tags":{"id":"tags","type":"multi_select","multi_select":[{"name":"Terminal"},{"name":"Notion"}]},
			"Done":{"id":"done","type":"checkbox","checkbox":true},
			"Owner":{"id":"owner","type":"people","people":[{"id":"person","name":"Ada"}]},
			"Due":{"id":"due","type":"date","date":{"start":"2026-09-25","end":null}}
		}
	}`), &raw); err != nil {
		t.Fatal(err)
	}
	page := raw.page()
	if page.Title != "Ship ntty" || page.ParentKind != "data_source_id" || page.ParentID != "source" {
		t.Fatalf("row identity: %+v", page)
	}
	properties := page.PropertyValues()
	values := map[string]Property{}
	for _, property := range properties {
		values[property.Name] = property
	}
	for name, want := range map[string]string{"Status": "In progress", "Tags": "Terminal, Notion", "Done": "✓", "Owner": "Ada", "Due": "2026-09-25"} {
		if values[name].Text != want {
			t.Errorf("%s: %q want %q", name, values[name].Text, want)
		}
	}
	if len(properties) == 0 || properties[0].Type != "title" {
		t.Fatalf("title property was not first: %+v", properties)
	}
}

func TestIncompletePatchResponseRetainsMergedAttempt(t *testing.T) {
	c := NewClient()
	c.interval = 0
	base, local, remote := "First\nLast", "First local\nLast", "First\nLast remote"
	want := "First local\nLast remote"
	writes := 0
	c.run = func(_ context.Context, args []string, _ []byte) ([]byte, error) {
		if args[3] == "GET" {
			return json.Marshal(Content{Object: "page_markdown", Markdown: remote})
		}
		writes++
		remote = want
		return json.Marshal(Content{Object: "page_markdown", Markdown: "First local", Truncated: true})
	}
	_, err := c.Save(context.Background(), "p", base, local)
	var attempt *SaveAttemptError
	if !errors.As(err, &attempt) || !errors.Is(err, ErrIncomplete) || attempt.Base.Markdown != "First\nLast remote" || attempt.Text != want {
		t.Fatalf("incomplete response lost the exact merged attempt: %#v %v", attempt, err)
	}
	result, err := c.Save(context.Background(), "p", attempt.Base.Markdown, attempt.Text)
	if err != nil || result.Markdown != want || writes != 1 {
		t.Fatalf("reconciliation repeated the write: %+v %v writes=%d", result, err, writes)
	}
}

func TestRequestPacingCancellationAndRetryPolicy(t *testing.T) {
	c := NewClient()
	c.interval = 8 * time.Millisecond
	var starts []time.Time
	c.run = func(context.Context, []string, []byte) ([]byte, error) {
		starts = append(starts, time.Now())
		return []byte(`{"object":"page_markdown","markdown":""}`), nil
	}
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Read(context.Background(), "p"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	for i := 1; i < len(starts); i++ {
		if starts[i].Sub(starts[i-1]) < c.interval-time.Millisecond {
			t.Fatal("requests burst instead of being paced")
		}
	}
	c.gate <- struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := c.Read(ctx, "p")
	<-c.gate
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("queued call ignored cancellation: %v", err)
	}
	if retryDelay(errors.New(`429 rate_limited Retry-After: 12`), 0, false) != 12*time.Second {
		t.Fatal("Retry-After was ignored")
	}
	if retryDelay(errors.New("503 service_unavailable"), 0, false) != 0 {
		t.Fatal("ambiguous writes must not be automatically retried")
	}
	if retryDelay(errors.New("503 service_unavailable"), 0, true) == 0 {
		t.Fatal("read-only transient failures should back off")
	}
	if retryDelay(errors.New("401 unauthorized"), 0, true) != 0 {
		t.Fatal("authentication errors must not loop")
	}
}

func TestCreateSeedsBlankPageAndEditableValidatesMarkdown(t *testing.T) {
	c := NewClient()
	c.interval = 0
	calls := 0
	c.run = func(_ context.Context, args []string, body []byte) ([]byte, error) {
		calls++
		if args[3] == "POST" {
			var request struct {
				Markdown string `json:"markdown"`
			}
			if err := json.Unmarshal(body, &request); err != nil {
				return nil, err
			}
			if request.Markdown != "<empty-block/>" {
				return nil, fmt.Errorf("blank page was created without a safe anchor: %q", request.Markdown)
			}
			return []byte(`{"object":"page","id":"p"}`), nil
		}
		return []byte(`{"object":"page_markdown","id":"p","markdown":"<empty-block/>"}`), nil
	}
	_, content, err := c.Create(context.Background(), "", "Blank", "")
	if err != nil || calls != 2 || !content.Editable() {
		t.Fatalf("create: %+v, %v (%d calls)", content, err, calls)
	}
	for _, content := range []Content{
		{Markdown: "text"},
		{Object: "page_markdown", Markdown: "<unknown"},
		{Object: "page_markdown", Markdown: "text", Truncated: true},
	} {
		if content.Editable() {
			t.Fatalf("unsafe content reported editable: %+v", content)
		}
	}
}

func TestRenameUsesDiscoveredTitleProperty(t *testing.T) {
	c := NewClient()
	c.interval = 0
	c.run = func(_ context.Context, args []string, body []byte) ([]byte, error) {
		if args[1] != "v1/pages/p" || args[3] != "PATCH" {
			return nil, fmt.Errorf("unexpected request: %v", args)
		}
		var request struct {
			Properties map[string]struct {
				Title []struct {
					Text struct {
						Content string `json:"content"`
					} `json:"text"`
				} `json:"title"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			return nil, err
		}
		value, ok := request.Properties["prop-id"]
		if !ok || len(value.Title) != 1 || value.Title[0].Text.Content != "Renamed" {
			return nil, fmt.Errorf("wrong rename body: %s", body)
		}
		return []byte(`{"object":"page","id":"p","properties":{"Whatever":{"id":"prop-id","type":"title","title":[{"plain_text":"Renamed"}]}}}`), nil
	}
	page, err := c.Rename(context.Background(), Page{ID: "p", Title: "Old", TitleProperty: "prop-id"}, "Renamed")
	if err != nil || page.Title != "Renamed" || page.TitleProperty != "prop-id" {
		t.Fatalf("rename: %+v %v", page, err)
	}
}
