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
