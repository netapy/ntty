package notion

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// CommentsBackend is optional so existing Backend wrappers do not accidentally
// advertise comment support through an embedded Backend.
type CommentsBackend interface {
	ListComments(context.Context, string, string) (CommentListing, error)
	// Exactly one of pageID and discussionID must be nonempty.
	CreateComment(ctx context.Context, pageID, discussionID, markdown string) (Comment, error)
}

type Comment struct {
	ID, DiscussionID, Text, Author, AuthorID string
	Created, Edited                          string
	Attachments                              int
	OriginalContentDeleted                   bool
}

type CommentListing struct {
	Comments   []Comment
	Cursor     string
	Incomplete bool
}

type apiComment struct {
	Object       string     `json:"object"`
	ID           string     `json:"id"`
	DiscussionID string     `json:"discussion_id"`
	Created      string     `json:"created_time"`
	Edited       string     `json:"last_edited_time"`
	RichText     []richText `json:"rich_text"`
	CreatedBy    struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"created_by"`
	DisplayName struct {
		Name string `json:"resolved_name"`
	} `json:"display_name"`
	Attachments            []struct{} `json:"attachments"`
	OriginalContentDeleted bool       `json:"original_content_deleted"`
}

func (c apiComment) comment() Comment {
	var text strings.Builder
	for _, part := range c.RichText {
		if part.Plain != "" {
			text.WriteString(part.Plain)
		} else {
			text.WriteString(part.Text.Content)
		}
	}
	author := c.DisplayName.Name
	if author == "" {
		author = c.CreatedBy.Name
	}
	return Comment{ID: c.ID, DiscussionID: c.DiscussionID, Text: text.String(), Author: author, AuthorID: c.CreatedBy.ID, Created: c.Created, Edited: c.Edited, Attachments: len(c.Attachments), OriginalContentDeleted: c.OriginalContentDeleted}
}

// ListComments returns one page of unresolved comments on this page/block.
// Child blocks require their own requests; the API has no resolved-history feed.
func (c *Client) ListComments(ctx context.Context, id, cursor string) (CommentListing, error) {
	if id == "" {
		return CommentListing{}, fmt.Errorf("comments need a page or block ID")
	}
	query := url.Values{"block_id": {id}, "page_size": {"50"}}
	if cursor != "" {
		query.Set("start_cursor", cursor)
	}
	var raw struct {
		Object  string       `json:"object"`
		Results []apiComment `json:"results"`
		Cursor  string       `json:"next_cursor"`
		More    bool         `json:"has_more"`
		Status  struct {
			Type string `json:"type"`
		} `json:"request_status"`
	}
	if err := c.api(ctx, "GET", "v1/comments?"+query.Encode(), nil, true, &raw); err != nil {
		return CommentListing{}, err
	}
	if raw.Object != "list" || (raw.More && (raw.Cursor == "" || raw.Cursor == cursor)) {
		return CommentListing{}, fmt.Errorf("unexpected comments pagination response")
	}
	l := CommentListing{Incomplete: raw.Status.Type == "incomplete"}
	for _, item := range raw.Results {
		if item.Object != "comment" || item.ID == "" || item.DiscussionID == "" {
			return CommentListing{}, fmt.Errorf("unexpected comment response")
		}
		l.Comments = append(l.Comments, item.comment())
	}
	if raw.More {
		l.Cursor = raw.Cursor
	}
	return l, nil
}

func validateComment(pageID, discussionID, markdown string) error {
	if (pageID == "") == (discussionID == "") {
		return fmt.Errorf("choose a page or an existing discussion for the comment")
	}
	if strings.TrimSpace(markdown) == "" {
		return fmt.Errorf("write a comment first")
	}
	return nil
}

func (c *Client) CreateComment(ctx context.Context, pageID, discussionID, markdown string) (Comment, error) {
	if err := validateComment(pageID, discussionID, markdown); err != nil {
		return Comment{}, err
	}
	body := map[string]any{"markdown": markdown}
	if discussionID != "" {
		body["discussion_id"] = discussionID
	} else {
		body["parent"] = map[string]string{"page_id": pageID}
	}
	var raw apiComment
	// A failed non-idempotent write is never retried for ambiguous server errors.
	if err := c.api(ctx, "POST", "v1/comments", body, false, &raw); err != nil {
		return Comment{}, err
	}
	if raw.Object != "comment" || raw.ID == "" {
		return Comment{}, fmt.Errorf("comment response missing its ID; check Notion before retrying")
	}
	// Write-only connections legitimately receive only {object, id}. This is a
	// successful send, not a reason to retain/re-submit the draft.
	return raw.comment(), nil
}

func (d *Demo) pageComments(id string) []Comment {
	if d.comments == nil {
		d.comments = map[string][]Comment{}
	}
	if comments, ok := d.comments[id]; ok {
		return comments
	}
	comments := []Comment{}
	if id == "demo-welcome" {
		at := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
		comments = []Comment{
			{ID: "demo-comment-1", DiscussionID: "demo-discussion-1", Author: "Alex", Text: "A small place for feedback. Select this discussion to reply.", Created: at.Format(time.RFC3339)},
			{ID: "demo-comment-2", DiscussionID: "demo-discussion-1", Author: "Sam", Text: "Replies stay with their discussion, and new page comments have their own draft.", Created: at.Add(time.Minute).Format(time.RFC3339)},
			{ID: "demo-comment-3", DiscussionID: "demo-discussion-2", Author: "Alex", Text: "This is a second discussion. Nothing in the demo is sent to Notion.", Created: at.Add(2 * time.Minute).Format(time.RFC3339)},
		}
	}
	d.comments[id] = comments
	return comments
}

func (d *Demo) ListComments(ctx context.Context, id, cursor string) (CommentListing, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return CommentListing{}, err
	}
	if _, ok := d.docs[id]; !ok {
		return CommentListing{}, fmt.Errorf("page not found")
	}
	comments := d.pageComments(id)
	start := 0
	if cursor != "" {
		var err error
		start, err = strconv.Atoi(cursor)
		if err != nil || start < 0 || start > len(comments) {
			return CommentListing{}, fmt.Errorf("invalid comments cursor")
		}
	}
	// Small demo batches make the explicit More control easy to exercise.
	end := min(start+2, len(comments))
	l := CommentListing{Comments: append([]Comment(nil), comments[start:end]...)}
	if end < len(comments) {
		l.Cursor = strconv.Itoa(end)
	}
	return l, nil
}

func (d *Demo) CreateComment(ctx context.Context, pageID, discussionID, markdown string) (Comment, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Comment{}, err
	}
	if err := validateComment(pageID, discussionID, markdown); err != nil {
		return Comment{}, err
	}
	if discussionID != "" {
		for id := range d.docs {
			for _, comment := range d.pageComments(id) {
				if comment.DiscussionID == discussionID {
					pageID = id
					break
				}
			}
		}
	}
	if _, ok := d.docs[pageID]; !ok {
		return Comment{}, fmt.Errorf("page or discussion not found")
	}
	comments := d.pageComments(pageID)
	id := fmt.Sprintf("demo-comment-%d", time.Now().UnixNano())
	if discussionID == "" {
		discussionID = "discussion-" + id
	}
	comment := Comment{ID: id, DiscussionID: discussionID, Text: markdown, Author: "You (demo)", Created: time.Now().Format(time.RFC3339)}
	d.comments[pageID] = append(comments, comment)
	return comment, nil
}

var _ CommentsBackend = (*Client)(nil)
var _ CommentsBackend = (*Demo)(nil)
