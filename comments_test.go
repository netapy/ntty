package main

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"ntty/internal/notion"
	"ntty/internal/store"
)

type commentsTestBackend struct {
	notion.Backend
	list   func(context.Context, string, string) (notion.CommentListing, error)
	create func(context.Context, string, string, string) (notion.Comment, error)
}

func (b *commentsTestBackend) ListComments(ctx context.Context, id, cursor string) (notion.CommentListing, error) {
	return b.list(ctx, id, cursor)
}

func (b *commentsTestBackend) CreateComment(ctx context.Context, page, discussion, text string) (notion.Comment, error) {
	return b.create(ctx, page, discussion, text)
}

func newCommentsTestApp(t *testing.T, backend notion.Backend) (*app, func(func() bool)) {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := newApp(backend, s, store.State{}, nil, "", true)
	a.active = "demo-welcome"
	a.docs[a.active] = &notion.Doc{Page: notion.Page{ID: a.active, Title: "Comments test", Kind: "page"}}
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	a.ui.SetScreen(screen)
	screen.SetSize(120, 40)
	done := make(chan error, 1)
	go func() { done <- a.ui.Run() }()
	t.Cleanup(func() {
		a.ui.QueueUpdateDraw(func() { a.cancel(); a.ui.Stop() })
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	await := func(test func() bool) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			ok := false
			a.ui.QueueUpdateDraw(func() { ok = test() })
			if ok {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatal("comments UI did not reach expected state")
	}
	return a, await
}

func TestCommentsMoreDraftsAndFailedSend(t *testing.T) {
	demo := notion.NewDemo(nil)
	var lists, sends atomic.Int32
	type request struct{ page, discussion, text string }
	type result struct {
		comment notion.Comment
		err     error
	}
	started := make(chan request, 4)
	release := make(chan result, 4)
	b := &commentsTestBackend{Backend: demo}
	b.list = func(ctx context.Context, id, cursor string) (notion.CommentListing, error) {
		lists.Add(1)
		list, err := demo.ListComments(ctx, id, cursor)
		if cursor != "" && err == nil {
			first, _ := demo.ListComments(ctx, id, "")
			duplicate := first.Comments[1]
			duplicate.Text = "Updated text on an overlapping pagination boundary"
			list.Comments = append([]notion.Comment{duplicate}, list.Comments...)
		}
		return list, err
	}
	b.create = func(ctx context.Context, page, discussion, text string) (notion.Comment, error) {
		sends.Add(1)
		started <- request{page, discussion, text}
		select {
		case r := <-release:
			return r.comment, r.err
		case <-ctx.Done():
			return notion.Comment{}, ctx.Err()
		}
	}
	a, await := newCommentsTestApp(t, b)
	a.ui.QueueUpdateDraw(a.comments)
	await(func() bool { return !a.commentState.view.loading })
	var v *commentsView
	a.ui.QueueUpdateDraw(func() {
		v = a.commentState.view
		if len(v.state.items) != 2 || lists.Load() != 1 || v.state.cursor == "" {
			t.Errorf("comments auto-paginated: %d items, %d calls", len(v.state.items), lists.Load())
		}
		v.list.SetCurrentItem(1)
		if !strings.Contains(v.thread.GetText(false), "Alex · ") || !strings.Contains(v.thread.GetText(false), "20 Sep 2026") {
			t.Error("author and creation time are missing from discussion")
		}
		v.input.SetText("reply draft café", true)
		v.list.SetCurrentItem(0)
		v.input.SetText("separate page draft", true)
		v.list.SetCurrentItem(1)
		if v.input.GetText() != "reply draft café" {
			t.Error("changing discussion lost its draft")
		}
		// Native mouse selection/focus and Tab traversal reach the explicit More.
		x, y, _, _ := v.input.GetInnerRect()
		v.input.MouseHandler()(tview.MouseLeftDown, tcell.NewEventMouse(x, y, tcell.Button1, tcell.ModNone), func(p tview.Primitive) { a.ui.SetFocus(p) })
	})
	a.ui.QueueEvent(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone))
	await(func() bool { return a.ui.GetFocus() == v.more })
	a.ui.QueueEvent(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
	await(func() bool { return !v.loading && lists.Load() == 2 })
	a.ui.QueueUpdateDraw(func() {
		if len(v.state.items) != 3 || v.state.cursor != "" || !strings.Contains(v.thread.GetText(false), "Updated text") {
			t.Errorf("pagination did not merge overlapping comments: %+v", v.state.items)
		}
		if v.input.GetText() != "reply draft café" {
			t.Error("loading another page changed the composer")
		}
		a.ui.SetFocus(v.input)
	})
	a.ui.QueueEvent(tcell.NewEventKey(tcell.KeyCtrlS, 0, tcell.ModNone))
	a.ui.QueueEvent(tcell.NewEventKey(tcell.KeyCtrlS, 0, tcell.ModNone))
	select {
	case req := <-started:
		if req.page != "" || req.discussion != "demo-discussion-1" || req.text != "reply draft café" {
			t.Fatalf("reply was redirected or changed: %+v", req)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("explicit send did not start")
	}
	a.ui.QueueUpdateDraw(func() {
		if !v.state.sending || !v.input.GetDisabled() || !a.commentsSending() || sends.Load() != 1 {
			t.Error("in-flight send did not disable editing/double submit")
		}
		v.input.InputHandler()(tcell.NewEventKey(tcell.KeyRune, 'X', tcell.ModNone), func(tview.Primitive) {})
		if v.input.GetText() != "reply draft café" {
			t.Error("typing changed the sending snapshot")
		}
		a.closeModal()
		a.comments()
		v = a.commentState.view
		if !v.input.GetDisabled() || v.input.GetText() != "reply draft café" || lists.Load() != 2 {
			t.Error("close/reopen lost the in-flight draft or bypassed cache")
		}
		a.sendComment(v)
		// Simulate Quit waiting for this send; an error must keep the session
		// alive because comment drafts are deliberately session-local.
		a.quitting = true
	})
	release <- result{err: errors.New("offline")}
	await(func() bool { return !v.state.sending })
	a.ui.QueueUpdateDraw(func() {
		if v.input.GetText() != "reply draft café" || v.input.GetDisabled() || sends.Load() != 1 || a.quitting || !strings.Contains(v.status.GetText(false), "draft kept") {
			t.Error("failed send lost draft, stayed disabled, or retried automatically")
		}
		a.closeModal()
		a.comments()
		v = a.commentState.view
		if v.input.GetText() != "reply draft café" {
			t.Error("failed draft did not survive reopening")
		}
		a.sendComment(v)
	})
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("explicit retry did not start")
	}
	// Successful write-only responses clear the sent draft without resubmitting.
	release <- result{comment: notion.Comment{ID: "partial-success"}}
	await(func() bool { return !v.state.sending })
	a.ui.QueueUpdateDraw(func() {
		if v.input.GetText() != "" || v.state.drafts[""] != "separate page draft" || sends.Load() != 2 || !strings.Contains(v.status.GetText(false), "Sent to Notion") {
			t.Error("partial success did not clear exactly the sent draft")
		}
		a.sendComment(v)
		if sends.Load() != 2 {
			t.Error("empty successful draft was sent twice")
		}
	})
}

func TestCommentsIgnoreClosedDialogRead(t *testing.T) {
	type pendingRead struct {
		ctx     context.Context
		release chan notion.CommentListing
	}
	requests := make(chan pendingRead, 2)
	returned := make(chan struct{}, 2)
	b := &commentsTestBackend{Backend: notion.NewDemo(nil)}
	b.list = func(ctx context.Context, _, _ string) (notion.CommentListing, error) {
		request := pendingRead{ctx: ctx, release: make(chan notion.CommentListing, 1)}
		requests <- request
		// Deliberately return a result after cancellation, like a late response.
		result := <-request.release
		returned <- struct{}{}
		return result, nil
	}
	a, await := newCommentsTestApp(t, b)
	a.ui.QueueUpdateDraw(a.comments)
	first := <-requests
	a.ui.QueueUpdateDraw(func() { a.closeModal(); a.comments() })
	second := <-requests
	if first.ctx.Err() != context.Canceled {
		t.Error("closing comments did not cancel the read")
	}
	second.release <- notion.CommentListing{Comments: []notion.Comment{{ID: "new", DiscussionID: "new-thread", Text: "Current dialog"}}}
	<-returned
	await(func() bool { return !a.commentState.view.loading && len(a.commentState.view.state.items) == 1 })
	first.release <- notion.CommentListing{Comments: []notion.Comment{{ID: "old", DiscussionID: "old-thread", Text: "Stale dialog"}}}
	<-returned
	// Give the stale result's queued UI callback an opportunity to run.
	deadline := time.Now().Add(50 * time.Millisecond)
	for time.Now().Before(deadline) {
		a.ui.QueueUpdateDraw(func() {
			if items := a.commentState.view.state.items; len(items) != 1 || items[0].ID != "new" {
				t.Errorf("closed view overwrote current results: %+v", items)
			}
		})
		time.Sleep(5 * time.Millisecond)
	}
}

func TestCommentsRefreshPreservesUnavailableReply(t *testing.T) {
	demo := notion.NewDemo(nil)
	var lists, sends atomic.Int32
	b := &commentsTestBackend{Backend: demo}
	b.list = func(ctx context.Context, id, cursor string) (notion.CommentListing, error) {
		switch lists.Add(1) {
		case 1:
			return demo.ListComments(ctx, id, cursor)
		case 2:
			return notion.CommentListing{}, errors.New("cannot read comments")
		default:
			return notion.CommentListing{}, nil
		}
	}
	b.create = func(context.Context, string, string, string) (notion.Comment, error) {
		sends.Add(1)
		return notion.Comment{}, errors.New("must not send")
	}
	a, await := newCommentsTestApp(t, b)
	a.ui.QueueUpdateDraw(a.comments)
	await(func() bool { return !a.commentState.view.loading })
	var v *commentsView
	a.ui.QueueUpdateDraw(func() {
		v = a.commentState.view
		v.list.SetCurrentItem(1)
		v.input.SetText("keep this reply", true)
		a.loadComments(v, false)
	})
	await(func() bool { return !v.loading })
	a.ui.QueueUpdateDraw(func() {
		if len(v.state.items) != 2 || v.state.cursor == "" || v.input.GetText() != "keep this reply" {
			t.Error("failed refresh lost cached pagination or the reply draft")
		}
		a.loadComments(v, false)
	})
	await(func() bool { return !v.loading })
	a.ui.QueueUpdateDraw(func() {
		index := v.list.GetCurrentItem()
		if v.input.GetText() != "keep this reply" || v.discussions[index] != "demo-discussion-1" || v.canReply() {
			t.Error("missing discussion redirected or lost the reply draft")
		}
		a.sendComment(v)
		if sends.Load() != 0 {
			t.Error("unavailable reply was sent as a new page comment")
		}
		v.list.SetCurrentItem(0)
		v.list.SetCurrentItem(index)
		if v.input.GetText() != "keep this reply" {
			t.Error("unavailable reply draft could not be revisited")
		}
	})
}

func TestCommentsUnavailableBackendDoesNotPanic(t *testing.T) {
	// Embedding the original Backend must not promote unimplemented comments.
	b := struct{ notion.Backend }{notion.NewDemo(nil)}
	a, _ := newCommentsTestApp(t, b)
	a.ui.QueueUpdateDraw(func() {
		a.comments()
		if a.modal || !strings.Contains(a.status.GetText(false), "not available") {
			t.Error("backend without optional comments interface was not handled")
		}
	})
}
