package main

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"ntty/internal/notion"
)

// The app owns one commentsState. Drafts and successful sends survive closing
// the overlay for this session; an old read must never update a new overlay.
type commentsState struct {
	pages map[string]*pageComments
	view  *commentsView
}

type pageComments struct {
	items      []notion.Comment
	cursor     string
	fetched    time.Time
	incomplete bool
	drafts     map[string]string // Empty discussion ID means a new page comment.
	target     string
	sending    bool
	notice     string
}

type commentsView struct {
	pane                *tview.Flex
	overlay             tview.Primitive
	list                *tview.List
	thread, status      *tview.TextView
	input               *tview.TextArea
	more, refresh, send *tview.Button
	close               *tview.Button
	backend             notion.CommentsBackend
	pageID              string
	state               *pageComments
	discussions         []string
	loading, setting    bool
	cancel              context.CancelFunc
	generation          int
}

func (a *app) comments() {
	if a.modal {
		return
	}
	d := a.docs[a.active]
	if d == nil {
		a.message("Open a page to view comments", false)
		return
	}
	backend, ok := a.backend.(notion.CommentsBackend)
	if !ok {
		a.message("Comments are not available for this backend", true)
		return
	}
	if a.commentState.pages == nil {
		a.commentState.pages = map[string]*pageComments{}
	}
	p := a.commentState.pages[a.active]
	if p == nil {
		p = &pageComments{drafts: map[string]string{}}
		a.commentState.pages[a.active] = p
	}
	v := &commentsView{backend: backend, pageID: a.active, state: p}
	v.list = tview.NewList().ShowSecondaryText(true).SetHighlightFullLine(false).SetMainTextStyle(tcell.StyleDefault).SetSecondaryTextStyle(quiet).SetSelectedStyle(selection)
	v.list.SetBorderPadding(0, 0, 0, 1)
	v.thread = tview.NewTextView().SetWrap(true).SetWordWrap(true)
	v.thread.SetBorderPadding(0, 0, 1, 0)
	v.status = tview.NewTextView().SetWrap(true).SetWordWrap(true).SetTextStyle(quiet)
	v.input = tview.NewTextArea().SetWordWrap(true).SetPlaceholder("Write a comment… Inline Markdown is supported.").SetTextStyle(tcell.StyleDefault).SetSelectedStyle(selection).SetPlaceholderStyle(quiet)
	v.input.SetBorder(true).SetBorderStyle(quiet).SetBorderPadding(0, 0, 1, 1)
	if _, err := exec.LookPath("pbcopy"); err == nil {
		v.input.SetClipboard(func(text string) {
			cmd := exec.Command("pbcopy")
			cmd.Stdin = strings.NewReader(text)
			if err := cmd.Run(); err != nil {
				p.notice = "Clipboard: " + err.Error()
				a.updateCommentControls(v)
			}
		}, func() string {
			out, err := exec.Command("pbpaste").Output()
			if err != nil {
				p.notice = "Clipboard: " + err.Error()
				a.updateCommentControls(v)
			}
			return string(out)
		})
	}
	v.input.SetChangedFunc(func() {
		if !v.setting {
			p.drafts[p.target] = v.input.GetText()
			a.updateCommentControls(v)
		}
	})
	v.list.SetChangedFunc(func(i int, _, _ string, _ rune) {
		if v.setting || p.sending || i < 0 || i >= len(v.discussions) {
			return
		}
		p.target = v.discussions[i]
		a.showCommentDiscussion(v)
		v.thread.ScrollToBeginning()
	})
	v.list.SetSelectedFunc(func(int, string, string, rune) {
		if !p.sending {
			a.ui.SetFocus(v.input)
		}
	})
	v.list.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if p.sending {
			return nil
		}
		return e
	})
	v.list.SetMouseCapture(func(action tview.MouseAction, e *tcell.EventMouse) (tview.MouseAction, *tcell.EventMouse) {
		if p.sending {
			return action, nil
		}
		return action, e
	})
	v.more = a.button("More", func() { a.loadComments(v, true) }).SetDisabledStyle(quiet)
	v.refresh = a.button("Refresh", func() { a.loadComments(v, false) }).SetDisabledStyle(quiet)
	v.send = a.button("Send", func() { a.sendComment(v) }).SetDisabledStyle(quiet)
	v.close = a.button("Close", a.closeModal)
	buttons := tview.NewFlex().AddItem(v.more, 0, 1, false).AddItem(v.refresh, 0, 1, false).AddItem(v.send, 0, 1, false).AddItem(v.close, 0, 1, false)
	hint := tview.NewTextView().SetText("Tab focus · Enter reply · Ctrl+S send · click Refresh · Esc close").SetTextStyle(quiet).SetWrap(false)
	body := tview.NewFlex().AddItem(v.list, 0, 1, true).AddItem(v.thread, 0, 2, false)
	v.pane = tview.NewFlex().SetDirection(tview.FlexRow).AddItem(body, 0, 1, true).AddItem(v.input, 6, 0, false).AddItem(v.status, 2, 0, false).AddItem(buttons, 1, 0, false).AddItem(hint, 1, 0, false)
	v.pane.Box = tview.NewBox()
	v.pane.SetBorder(true).SetBorderStyle(quiet).SetTitle(" Page comments · "+tview.Escape(d.Page.Title)+" ").SetBorderPadding(1, 0, 1, 1)
	v.pane.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		switch e.Key() {
		case tcell.KeyTab, tcell.KeyBacktab:
			a.focusComments(v, e.Key() == tcell.KeyBacktab)
			return nil
		case tcell.KeyCtrlS:
			a.sendComment(v)
			return nil
		case tcell.KeyCtrlC:
			if a.ui.GetFocus() == v.input {
				return tcell.NewEventKey(tcell.KeyCtrlQ, 0, tcell.ModNone)
			}
		}
		return e
	})
	a.commentState.view = v
	a.overlay(v.pane, 100, 32)
	v.overlay = a.layers.GetPage("modal")
	v.overlay.(*tview.Flex).SetMouseCapture(func(action tview.MouseAction, e *tcell.EventMouse) (tview.MouseAction, *tcell.EventMouse) {
		if action == tview.MouseLeftDown && !v.pane.InRect(e.Position()) {
			a.closeModal()
			return action, nil
		}
		return action, e
	})
	a.renderComments(v)
	if !p.sending && (p.fetched.IsZero() || time.Since(p.fetched) > time.Minute) {
		a.loadComments(v, false)
	}
}

// Call from closeModal. Do not cancel a POST: keep its outcome so reopening
// cannot submit the same in-flight draft a second time.
func (a *app) closeComments() {
	if v := a.commentState.view; v != nil {
		if v.cancel != nil {
			v.cancel()
		}
		v.generation++
		a.commentState.view = nil
	}
}

func (a *app) commentsSending() bool {
	for _, p := range a.commentState.pages {
		if p.sending {
			return true
		}
	}
	return false
}

func (a *app) commentsActive(v *commentsView) bool {
	return a.commentState.view == v && a.modal && a.layers.GetPage("modal") == v.overlay
}

func commentAuthor(c notion.Comment) string {
	if c.Author != "" {
		return c.Author
	}
	if c.AuthorID != "" {
		return "User " + c.AuthorID
	}
	return "Author unavailable"
}

func commentTime(value string) string {
	if at, err := time.Parse(time.RFC3339, value); err == nil {
		return at.Local().Format("02 Jan 2006 15:04")
	}
	return value
}

func (a *app) renderComments(v *commentsView) {
	p := v.state
	v.setting = true
	v.list.Clear()
	v.discussions = []string{""}
	v.list.AddItem("+ New page comment", "Open page comments only", 0, nil)
	seen := map[string]bool{"": true}
	for _, c := range p.items {
		if seen[c.DiscussionID] {
			continue
		}
		seen[c.DiscussionID] = true
		v.discussions = append(v.discussions, c.DiscussionID)
		preview := strings.Join(strings.Fields(c.Text), " ")
		if preview == "" {
			preview = "Discussion"
		}
		v.list.AddItem(tview.Escape(preview), tview.Escape(commentAuthor(c)+" · "+commentTime(c.Created)), 0, nil)
	}
	// Keep drafts addressable even if a refresh/pagination no longer includes
	// their discussion. Never silently redirect a reply into a page comment.
	var orphaned []string
	if !seen[p.target] {
		orphaned = append(orphaned, p.target)
		seen[p.target] = true
	}
	for id, text := range p.drafts {
		if !seen[id] && text != "" {
			orphaned = append(orphaned, id)
		}
	}
	sort.Strings(orphaned)
	for _, id := range orphaned {
		v.discussions = append(v.discussions, id)
		v.list.AddItem("Draft reply", "Discussion not loaded", 0, nil)
	}
	for i, id := range v.discussions {
		if id == p.target {
			v.list.SetCurrentItem(i)
			break
		}
	}
	v.setting = false
	a.showCommentDiscussion(v)
}

func (a *app) showCommentDiscussion(v *commentsView) {
	p := v.state
	var text strings.Builder
	if p.target == "" {
		text.WriteString("New page comment\n\nSelect a discussion to read it and reply, or write a new comment below.\n\nOnly open comments attached to this page are listed. Resolved comments and page version history are unavailable through the public API.\n\nDrafts are kept per discussion until you quit ntty.")
		v.input.SetTitle(" New page comment ")
	} else {
		v.input.SetTitle(" Reply to selected discussion ")
		for _, c := range p.items {
			if c.DiscussionID != p.target {
				continue
			}
			fmt.Fprintf(&text, "%s · %s\n%s\n", commentAuthor(c), commentTime(c.Created), c.Text)
			if c.Edited != "" && c.Edited != c.Created {
				fmt.Fprintf(&text, "Edited %s\n", commentTime(c.Edited))
			}
			if c.Attachments > 0 {
				fmt.Fprintf(&text, "%d attachment(s) · view in Notion\n", c.Attachments)
			}
			if c.OriginalContentDeleted {
				text.WriteString("Original commented content was deleted.\n")
			}
			text.WriteString("\n")
		}
		if text.Len() == 0 {
			text.WriteString("This discussion is not in the loaded comments. Use More or Refresh to find it. Your reply draft is kept.")
		}
	}
	v.thread.SetText(text.String())
	v.setting = true
	if v.input.GetText() != p.drafts[p.target] {
		v.input.SetText(p.drafts[p.target], false)
	}
	v.setting = false
	a.updateCommentControls(v)
}

func (v *commentsView) canReply() bool {
	if v.state.target == "" {
		return true
	}
	for _, c := range v.state.items {
		if c.DiscussionID == v.state.target {
			return true
		}
	}
	return false
}

func (a *app) updateCommentControls(v *commentsView) {
	p := v.state
	busy := v.loading || p.sending
	v.input.SetDisabled(p.sending)
	v.more.SetDisabled(busy || p.cursor == "")
	v.refresh.SetDisabled(busy)
	v.send.SetDisabled(busy || !v.canReply() || strings.TrimSpace(p.drafts[p.target]) == "")
	v.send.SetLabel("Send")
	switch {
	case p.sending:
		v.send.SetLabel("Sending…")
		v.status.SetText("Sending… Your draft is kept until Notion confirms.")
	case v.loading:
		v.status.SetText("Loading comments…")
	case p.notice != "":
		v.status.SetText(p.notice)
	default:
		v.status.SetText("No open comments loaded. Write a page comment below.")
	}
}

func (a *app) focusComments(v *commentsView, backwards bool) {
	items := []tview.Primitive{v.list, v.thread}
	if !v.state.sending {
		items = append(items, v.input)
		if !v.loading {
			if v.state.cursor != "" {
				items = append(items, v.more)
			}
			items = append(items, v.refresh)
			if v.canReply() && strings.TrimSpace(v.input.GetText()) != "" {
				items = append(items, v.send)
			}
		}
	}
	items = append(items, v.close)
	index := -1
	for i, item := range items {
		if item == a.ui.GetFocus() {
			index = i
			break
		}
	}
	if backwards {
		index = (index + len(items) - 1) % len(items)
	} else {
		index = (index + 1) % len(items)
	}
	a.ui.SetFocus(items[index])
}

func mergeComments(first, next []notion.Comment) []notion.Comment {
	out := append([]notion.Comment(nil), first...)
	positions := map[string]int{}
	for i, c := range out {
		positions[c.ID] = i
	}
	for _, c := range next {
		if i, ok := positions[c.ID]; ok {
			out[i] = c
		} else {
			positions[c.ID] = len(out)
			out = append(out, c)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		left, e1 := time.Parse(time.RFC3339, out[i].Created)
		right, e2 := time.Parse(time.RFC3339, out[j].Created)
		if e1 != nil || e2 != nil {
			return e1 == nil && e2 != nil
		}
		return left.Before(right)
	})
	return out
}

func (a *app) loadComments(v *commentsView, more bool) {
	if !a.commentsActive(v) || v.loading || v.state.sending || (more && v.state.cursor == "") {
		return
	}
	cursor := ""
	if more {
		cursor = v.state.cursor
	}
	ctx, cancel := context.WithCancel(a.ctx)
	v.cancel = cancel
	v.generation++
	generation := v.generation
	v.loading = true
	a.updateCommentControls(v)
	go func() {
		defer cancel()
		result, err := v.backend.ListComments(ctx, v.pageID, cursor)
		a.ui.QueueUpdateDraw(func() {
			if !a.commentsActive(v) || generation != v.generation {
				return
			}
			v.loading = false
			p := v.state
			if err != nil {
				p.notice = "Load: " + err.Error() + " · cached comments and drafts kept"
			} else {
				if more {
					p.items = mergeComments(p.items, result.Comments)
				} else {
					p.items = result.Comments
				}
				p.cursor, p.incomplete, p.fetched = result.Cursor, result.Incomplete, time.Now()
				var authors []notion.Person
				for _, comment := range result.Comments {
					authors = append(authors, notion.Person{ID: comment.AuthorID, Name: comment.Author})
				}
				a.rememberPeople(authors...)
				p.notice = fmt.Sprintf("%d open comments loaded", len(p.items))
				if p.cursor != "" {
					p.notice += " · More available"
				}
				if p.incomplete {
					p.notice += " · Notion limited these results"
				}
			}
			a.renderComments(v)
		})
	}()
}

func (a *app) sendComment(v *commentsView) {
	if !a.commentsActive(v) || v.state.sending || v.loading || !v.canReply() {
		return
	}
	p := v.state
	target, text := p.target, p.drafts[p.target]
	if strings.TrimSpace(text) == "" {
		return
	}
	pageID := ""
	if target == "" {
		pageID = v.pageID
	}
	p.sending = true
	a.updateCommentControls(v)
	go func() {
		comment, err := v.backend.CreateComment(a.ctx, pageID, target, text)
		a.ui.QueueUpdateDraw(func() {
			p.sending = false
			if err != nil {
				p.notice = "Send: " + err.Error() + " · draft kept; refresh/check Notion before retrying"
				if a.quitting {
					a.quitting = false
					a.message("Comment not sent; draft kept in Comments for this session", true)
				}
			} else {
				if p.drafts[target] == text {
					delete(p.drafts, target)
				}
				p.notice = "Sent to Notion"
				if comment.DiscussionID != "" {
					p.items = mergeComments(p.items, []notion.Comment{comment})
					p.target = comment.DiscussionID
				} else {
					p.notice += " · this connection returned only an ID; comment details unavailable"
				}
			}
			// Reconcile the send even if its overlay closed, but never touch the
			// closed view or an unrelated page's current dialog.
			if current := a.commentState.view; current != nil && current.state == p && a.commentsActive(current) {
				a.renderComments(current)
			}
			if a.quitting {
				a.quit()
			}
		})
	}()
}
