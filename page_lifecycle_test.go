package main

import (
	"ntty/internal/notion"
	"ntty/internal/store"
	"testing"
	"time"
)

func TestRemoteTrashHidesPageAndPreservesDraft(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := notion.Page{ID: "12345678-1234-4234-8234-123456789abc", Title: "Sample page", Kind: "page"}
	a := newApp(notion.NewDemo(nil), s, store.State{Pages: []notion.Page{p}, Pins: []notion.Page{p}, Recents: []notion.Page{p}}, nil, "", true)
	defer a.cancel()
	d := &notion.Doc{Page: p, Text: "Unsynced important text", Dirty: true}
	a.docs[p.ID] = d
	a.active = p.ID
	a.listed = []notion.Page{p}
	a.changed[p.ID] = time.Now()
	p.InTrash = true
	a.applyPageMetadata(p)
	if len(a.visible)+len(a.favoritePages)+len(a.recentPages) != 0 {
		t.Fatal("deleted page remains in navigation")
	}
	if d.Text != "Unsynced important text" || !d.Dirty || !a.blocked[p.ID] || !a.editor.GetDisabled() {
		t.Fatal("trash discarded draft or allowed editing")
	}
	if err := a.drafts.Flush(p.ID); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.LoadDoc(p.ID)
	if err != nil || loaded.Text != d.Text || !loaded.Page.InTrash {
		t.Fatal("trash state or draft not persisted")
	}
	a.open(p)
	if !a.modal {
		t.Fatal("opening cached trashed page must offer restore")
	}
}
