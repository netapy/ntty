package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"ntty/internal/notion"
	"ntty/internal/store"
)

type startupBackend struct {
	notion.Backend
	calls   chan string
	release chan struct{}
}

func (b *startupBackend) Read(ctx context.Context, id string) (notion.Content, error) {
	b.calls <- "read"
	select {
	case <-b.release:
		return notion.Content{Object: "page_markdown", ID: id, Markdown: "Cached content"}, nil
	case <-ctx.Done():
		return notion.Content{}, ctx.Err()
	}
}

func (b *startupBackend) Search(ctx context.Context, query, cursor string) (notion.Listing, error) {
	b.calls <- "search"
	return b.Backend.Search(ctx, query, cursor)
}

func (b *startupBackend) PagePath(context.Context, string) ([]notion.Page, error) {
	b.calls <- "path"
	return nil, nil
}

func (b *startupBackend) Page(ctx context.Context, id string) (notion.Page, error) {
	b.calls <- "metadata"
	return b.Backend.Page(ctx, id)
}

func TestStartupCachedPageBeforeBackgroundRequests(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := notion.Page{ID: "startup", Title: "Startup", Kind: "page", ParentKind: "workspace"}
	d := notion.Doc{Page: p, Text: "Cached content", Base: notion.Content{Object: "page_markdown", Markdown: "Cached content"}}
	if err := s.SaveDoc(d); err != nil {
		t.Fatal(err)
	}
	b := &startupBackend{Backend: notion.NewDemo(nil), calls: make(chan string, 20), release: make(chan struct{})}
	a := newApp(b, s, store.State{Pages: []notion.Page{p}, Last: p.ID}, nil, "", false)
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	a.ui.SetScreen(screen)
	screen.SetSize(100, 30)
	done := make(chan error, 1)
	go func() { done <- a.run() }()
	defer func() {
		a.ui.QueueUpdate(func() { a.cancel(); a.ui.Stop() })
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	select {
	case call := <-b.calls:
		if call != "read" {
			t.Fatalf("first request = %s, want document read", call)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("document read did not start")
	}
	a.ui.QueueUpdate(func() {
		if a.active != p.ID || a.editor.GetText() != d.Text {
			t.Error("cached document not ready during blocked refresh")
		}
		// Explicitly exercise the periodic background checks while Read blocks.
		a.startWorkspaceRefresh()
		a.refreshPageMetadata(time.Now())
	})
	select {
	case call := <-b.calls:
		t.Fatalf("background %s competed with document read", call)
	default:
	}
	close(b.release)
	want := map[string]bool{"path": false, "search": false}
	timeout := time.After(2 * time.Second)
	for !want["path"] || !want["search"] {
		select {
		case call := <-b.calls:
			want[call] = true
		case <-timeout:
			t.Fatal("background refresh did not resume")
		}
	}
}

func TestSidebarTrashIndexIncludesExpandedChildren(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	state := largeWorkspace()
	state.Pages[20].InTrash = true
	state.Pins = []notion.Page{state.Pages[20]}
	state.Pins[0].InTrash = false // Stale pinned metadata must not revive a page.
	state.ExpandedPages = []string{state.Pages[0].ID}
	a := newApp(notion.NewDemo(nil), s, state, nil, "", false)
	defer a.cancel()
	if len(a.favoritePages) != 0 {
		t.Fatal("trashed page appeared in favorites")
	}
	for _, p := range a.visible {
		if p.ID == state.Pages[20].ID {
			t.Fatal("trashed child appeared in expanded tree")
		}
	}
}

func largeWorkspace() store.State {
	s := store.State{}
	for i := 0; i < 8000; i++ {
		p := notion.Page{ID: fmt.Sprintf("00000000-0000-4000-8000-%012d", i), Title: "Project notes", Kind: "page", ParentKind: "page_id", ParentID: "00000000-0000-4000-8000-000000000000"}
		if i < 20 {
			p.ParentKind = "workspace"
			p.ParentID = ""
		}
		s.Pages = append(s.Pages, p)
	}
	return s
}

func BenchmarkStartupWorkspace(b *testing.B) {
	s, err := store.Open(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	state := largeWorkspace()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a := newApp(notion.NewDemo(nil), s, state, nil, "", false)
		a.cancel()
	}
}

func BenchmarkSidebarLargeWorkspace(b *testing.B) {
	s, err := store.Open(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	a := newApp(notion.NewDemo(nil), s, largeWorkspace(), nil, "", false)
	defer a.cancel()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.rebuildList()
	}
}
