package main

import (
	"context"
	"fmt"
	"testing"

	"ntty/internal/notion"
	"ntty/internal/store"
)

func TestDraftWriterFlushesNewestSnapshot(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	w := newDraftWriter(ctx, s)
	defer func() { cancel(); w.Wait() }()
	for i := 0; i < 200; i++ {
		w.Put(notion.Doc{Page: notion.Page{ID: "page", Title: "Page"}, Text: fmt.Sprintf("draft %d", i), Dirty: true})
	}
	if err := w.Flush("page"); err != nil {
		t.Fatal(err)
	}
	d, err := s.LoadDoc("page")
	if err != nil {
		t.Fatal(err)
	}
	if d.Text != "draft 199" {
		t.Fatalf("stale draft won: %q", d.Text)
	}
}

func TestStateWriterSnapshotsAndFlushesLatest(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	w := newDraftWriter(ctx, s)
	defer func() { cancel(); w.Wait() }()
	state := store.State{Pages: []notion.Page{{ID: "page"}}, DatabaseViews: map[string]string{"db": "first"}}
	for i := 0; i < 100; i++ {
		state.Pages[0].Title = fmt.Sprint(i)
		state.DatabaseViews["db"] = fmt.Sprint(i)
		w.PutState(state)
	}
	state.Pages[0].Title = "not queued"
	state.DatabaseViews["db"] = "not queued"
	w.Put(notion.Doc{Page: notion.Page{ID: "page"}, Text: "durable draft", Dirty: true})
	if err := w.FlushAll(); err != nil {
		t.Fatal(err)
	}
	saved, err := s.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if saved.Pages[0].Title != "99" || saved.DatabaseViews["db"] != "99" {
		t.Fatalf("mutable or stale state saved: %+v", saved)
	}
	if doc, err := s.LoadDoc("page"); err != nil || doc.Text != "durable draft" {
		t.Fatalf("draft lost: %+v %v", doc, err)
	}
}
