package notion

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Every API request goes through one cancellable gate, including retries and
// the read-before-write check. ntn owns authentication; no tokens are extracted.
type Client struct {
	gate     chan struct{}
	last     time.Time
	interval time.Duration
	run      func(context.Context, []string, []byte) ([]byte, error)
}

func NewClient() *Client {
	return &Client{gate: make(chan struct{}, 1), interval: 650 * time.Millisecond, run: runNTN}
}

func runNTN(ctx context.Context, args []string, body []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ntn", args...)
	if body != nil {
		cmd.Stdin = bytes.NewReader(body)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("ntn: %w", ctx.Err())
		}
		return nil, fmt.Errorf("ntn: %s %s", strings.TrimSpace(stderr.String()), strings.TrimSpace(string(out)))
	}
	return out, nil
}

func wait(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

var retryAfter = regexp.MustCompile(`(?i)retry[-_ ]after["\s:=]+([0-9]+)`)

func isRateLimit(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "rate_limited") || strings.Contains(s, "429") || strings.Contains(s, "rate limited")
}

func retryDelay(err error, attempt int, read bool) time.Duration {
	s := strings.ToLower(err.Error())
	rate := isRateLimit(err)
	transient := strings.Contains(s, "503") || strings.Contains(s, "502") || strings.Contains(s, "504") || strings.Contains(s, "service_unavailable")
	if !rate && !(read && transient) {
		return 0
	}
	d := time.Second * time.Duration(2<<attempt)
	if m := retryAfter.FindStringSubmatch(s); len(m) == 2 {
		n, _ := strconv.Atoi(m[1])
		if time.Duration(n)*time.Second > d {
			d = time.Duration(n) * time.Second
		}
	}
	return d
}

func (c *Client) api(ctx context.Context, method, path string, body any, read bool, dest any) error {
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	args := []string{"api", path, "-X", method}
	for attempt := 0; ; attempt++ {
		select {
		case c.gate <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
		if err := wait(ctx, time.Until(c.last.Add(c.interval))); err != nil {
			<-c.gate
			return err
		}
		c.last = time.Now()
		out, err := c.run(ctx, args, data)
		if err == nil {
			var e struct {
				Object, Code, Message string
				Status                int
			}
			if json.Unmarshal(out, &e) == nil && e.Object == "error" {
				err = fmt.Errorf("%s (%d): %s", e.Code, e.Status, e.Message)
			}
		}
		if err == nil {
			<-c.gate
			if err = json.Unmarshal(out, dest); err != nil {
				return fmt.Errorf("invalid ntn response: %w", err)
			}
			return nil
		}
		delay := retryDelay(err, attempt, read)
		// A server rate limit applies to every caller; a transient read's
		// retry sleep does not need to occupy the execution slot.
		if delay > 0 && isRateLimit(err) {
			c.last = time.Now().Add(delay - c.interval)
		}
		<-c.gate
		if attempt >= 3 || delay == 0 {
			return err
		}
		if err := wait(ctx, delay); err != nil {
			return err
		}
	}
}

func (c *Client) listing(ctx context.Context, path string, body map[string]any) (Listing, error) {
	var raw struct {
		Results []apiPage `json:"results"`
		Cursor  string    `json:"next_cursor"`
		More    bool      `json:"has_more"`
	}
	err := c.api(ctx, "POST", path, body, true, &raw)
	l := Listing{}
	for _, p := range raw.Results {
		l.Pages = append(l.Pages, p.page())
	}
	if raw.More {
		l.Cursor = raw.Cursor
	}
	return l, err
}

func (c *Client) Search(ctx context.Context, query, cursor string) (Listing, error) {
	body := map[string]any{"page_size": 100, "sort": map[string]string{"direction": "descending", "timestamp": "last_edited_time"}}
	if query != "" {
		body["query"] = query
	}
	if cursor != "" {
		body["start_cursor"] = cursor
	}
	return c.listing(ctx, "v1/search", body)
}

func (c *Client) Query(ctx context.Context, id, cursor string) (Listing, error) {
	body := map[string]any{"page_size": 50}
	if cursor != "" {
		body["start_cursor"] = cursor
	}
	return c.listing(ctx, "v1/data_sources/"+id+"/query", body)
}

func (c *Client) Page(ctx context.Context, id string) (Page, error) {
	var p apiPage
	err := c.api(ctx, "GET", "v1/pages/"+id, nil, true, &p)
	return p.page(), err
}

func (c *Client) Rename(ctx context.Context, page Page, title string) (Page, error) {
	property := page.TitleProperty
	if property == "" {
		fresh, err := c.Page(ctx, page.ID)
		if err != nil {
			return page, err
		}
		property = fresh.TitleProperty
	}
	if property == "" {
		return page, fmt.Errorf("page has no editable title property")
	}
	body := map[string]any{"properties": map[string]any{property: map[string]any{"title": []any{map[string]any{"text": map[string]string{"content": title}}}}}}
	var raw apiPage
	if err := c.api(ctx, "PATCH", "v1/pages/"+page.ID, body, false, &raw); err != nil {
		return page, err
	}
	renamed := raw.page()
	if renamed.ID == "" {
		return page, fmt.Errorf("rename returned no page ID")
	}
	return renamed, nil
}

func (c *Client) Read(ctx context.Context, id string) (Content, error) {
	var content Content
	err := c.api(ctx, "GET", "v1/pages/"+id+"/markdown", nil, true, &content)
	if err == nil && content.Object != "page_markdown" {
		err = fmt.Errorf("unexpected markdown response: %q", content.Object)
	}
	return content, err
}

func (c *Client) Save(ctx context.Context, id, base, text string) (Content, error) {
	return c.save(ctx, id, base, text, false)
}

// ReconcileSave confirms an uncertain write. If the page changed without a
// provable match for the attempted write, never turn that uncertainty into a
// new insertion. The persisted versions remain available for comparison.
func (c *Client) ReconcileSave(ctx context.Context, id, base, text string) (Content, error) {
	return c.save(ctx, id, base, text, true)
}

func (c *Client) save(ctx context.Context, id, base, text string, reconcile bool) (Content, error) {
	originalText := text
	remote, err := c.Read(ctx, id)
	if err != nil {
		return Content{}, err
	}
	if !remote.Editable() {
		return Content{}, ErrIncomplete
	}
	// Reconcile writes made by older builds before upgrading their blank-line
	// representation below.
	if remote.Markdown == originalText || sameSavedText(remote.Markdown, originalText) {
		remote.Normalized = remote.Markdown != originalText
		return remote, nil
	}
	text, err = encodeEditedEmptyBlocks(base, text)
	if err != nil {
		return remote, err
	}
	// Also reconciles an earlier successful write whose response was lost.
	// Notion may have removed Markdown-only blank separator lines while saving.
	if remote.Markdown == text || sameSavedText(remote.Markdown, text) {
		remote.Normalized = remote.Markdown != originalText
		return remote, nil
	}
	if remote.Markdown != base {
		base, text = alignTransientNotionURLs(base, text, remote.Markdown)
		if remote.Markdown == base {
			// Only transient Notion file signatures changed.
		} else if partialEmptyPageWrite(base, remote.Markdown, text) {
			// A previous empty-page replacement may have imported only a prefix.
			// Continue from that exact prefix without deleting any remote content.
			base = remote.Markdown
		} else {
			merged, ok, mergeErr := mergeNonOverlapping(base, text, remote.Markdown)
			if mergeErr != nil {
				return remote, mergeErr
			}
			if !ok {
				return remote, ErrConflict
			}
			if reconcile && !sameSavedText(merged, remote.Markdown) {
				return remote, ErrConflict
			}
			base, text = remote.Markdown, merged
		}
	}
	// The remote may already contain the local edit plus independent changes
	// (for example after a lost response followed by a collaborator edit). The
	// three-way merge then has nothing left to write.
	if base == text || sameSavedText(base, text) {
		remote.Normalized = remote.Markdown != originalText
		return remote, nil
	}
	updates, err := planContentUpdates(base, text)
	// Exact, unique targets protect the edited ranges between the read and write.
	// This is not a page-wide CAS: concurrent edits elsewhere are left alone.
	body := map[string]any{"type": "update_content", "update_content": map[string]any{
		"content_updates": updates, "allow_deleting_content": false,
	}}
	if err != nil {
		// A page beginning/ending with an opaque block has no editable anchor
		// for a new section outside it. Positional insertion adds only the new
		// blocks, never recreating that existing object. The complete original
		// page must remain an exact suffix/prefix, so this cannot hide deletions.
		position, added := "", ""
		if base != "" && errors.Is(err, ErrUnsafeUpdate) && !errors.Is(err, ErrProtectedContent) {
			if strings.HasSuffix(text, base) {
				position, added = "start", strings.TrimSuffix(text, base)
			} else if strings.HasPrefix(text, base) {
				position, added = "end", strings.TrimPrefix(text, base)
			}
		}
		if added == "" || position == "start" && !strings.HasSuffix(added, "\n") || position == "end" && !strings.HasPrefix(added, "\n") {
			return remote, err
		}
		if _, inspectErr := inspectMarkdown(added); inspectErr != nil {
			return remote, err
		}
		body = map[string]any{"type": "insert_content", "insert_content": map[string]any{
			"content": added, "position": map[string]string{"type": position},
		}}
	}
	var result Content
	err = c.api(ctx, "PATCH", "v1/pages/"+id+"/markdown", body, false, &result)
	if err != nil {
		return remote, &SaveAttemptError{Err: err, Base: remote, Text: text}
	}
	if err == nil && result.Object != "page_markdown" {
		err = fmt.Errorf("unexpected save response; draft retained")
	}
	if err == nil && !result.Editable() {
		err = ErrIncomplete
	}
	if err == nil && result.Markdown != text {
		if sameSavedText(result.Markdown, text) {
			// Notion omits empty Markdown separator lines between blocks. Its
			// response is the canonical version and must become the next save base.
			result.Normalized = true
		} else {
			alignedBase, alignedText := alignTransientNotionURLs(base, text, result.Markdown)
			merged, ok, mergeErr := mergeNonOverlapping(alignedBase, alignedText, result.Markdown)
			if mergeErr == nil && ok && merged == result.Markdown {
				// The exact local targets landed and an independent remote edit landed
				// during the write. Accept Notion's complete canonical response.
				result.Normalized = true
			} else {
				// A collaborator may have edited the same range, or Notion may have
				// dropped part of the write. Keep the draft rather than guessing.
				err = fmt.Errorf("Notion returned different content after saving; draft retained")
			}
		}
	}
	if err == nil && result.Markdown != originalText {
		result.Normalized = true
	}
	if err != nil {
		// A successful HTTP response with partial or unexpected content is just
		// as uncertain as a lost response. Persist the merged preflight pair so
		// the next attempt reconciles it before applying any newer typing.
		return result, &SaveAttemptError{Err: err, Base: remote, Text: text}
	}
	return result, nil
}

var notionNumberedList = regexp.MustCompile(`^\d+[.)] `)

func sameNotionText(a, b string) bool {
	compact := func(markdown string) string {
		var lines []string
		fence := ""
		listDepth := -1
		for _, line := range strings.Split(markdown, "\n") {
			trim := strings.TrimSpace(line)
			if strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~") {
				marker := trim[:3]
				if fence == "" {
					fence = marker
				} else if strings.HasPrefix(trim, fence) {
					fence = ""
				}
			}
			if fence == "" && trim == "" {
				continue
			}
			if fence == "" {
				line = strings.TrimRight(line, " \t")
				// Notion disambiguates adjacent literal dollars with an empty
				// code span, and collapses skipped list indentation levels.
				line = canonicalLiteralDollars(line)
				body := strings.TrimLeft(line, "\t")
				depth := len(line) - len(body)
				if strings.HasPrefix(body, "- ") || notionNumberedList.MatchString(body) {
					if listDepth >= 0 {
						depth = min(depth, listDepth+1)
					}
					line = strings.Repeat("\t", depth) + body
					listDepth = depth
				} else {
					listDepth = -1
				}
			}
			lines = append(lines, line)
		}
		return strings.Join(lines, "\n")
	}
	return compact(a) == compact(b)
}

func canonicalLiteralDollars(line string) string {
	var out strings.Builder
	for i := 0; i < len(line); {
		switch {
		case strings.HasPrefix(line[i:], "$``$"):
			out.WriteString("$$")
			i += 4
		case strings.HasPrefix(line[i:], `\$`):
			out.WriteByte('$')
			i += 2
		case line[i] == '\\' && i+1 < len(line):
			out.WriteString(line[i : i+2])
			i += 2
		case line[i] == '`':
			end := markdownLiteralEnd(line, i)
			if end > i {
				out.WriteString(line[i:end])
				i = end
			} else {
				out.WriteByte(line[i])
				i++
			}
		default:
			out.WriteByte(line[i])
			i++
		}
	}
	return out.String()
}

func (c *Client) Create(ctx context.Context, parent, title, text string) (Page, Content, error) {
	p := map[string]any{"workspace": true}
	if parent != "" {
		parts := strings.SplitN(parent, ":", 2)
		if len(parts) != 2 {
			return Page{}, Content{}, fmt.Errorf("parent must be page:<id> or data-source:<id>")
		}
		key := strings.ReplaceAll(parts[0], "-", "_") + "_id"
		p = map[string]any{key: parts[1]}
	}
	initial := text
	if initial == "" {
		initial = "<empty-block/>"
	}
	body := map[string]any{"parent": p, "properties": map[string]any{"title": map[string]any{"title": []any{map[string]any{"text": map[string]string{"content": title}}}}}, "markdown": initial}
	var raw apiPage
	if err := c.api(ctx, "POST", "v1/pages", body, false, &raw); err != nil {
		return Page{}, Content{}, err
	}
	page := raw.page()
	if page.ID == "" {
		return Page{}, Content{}, fmt.Errorf("create returned no page ID; check Notion before retrying")
	}
	content, err := c.Read(ctx, page.ID)
	return page, content, err
}
