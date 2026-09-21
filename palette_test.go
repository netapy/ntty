package main

import (
	"reflect"
	"testing"

	"github.com/rivo/tview"
	"ntty/internal/notion"
	"ntty/internal/store"
)

func TestSearchMatcher(t *testing.T) {
	for _, tc := range []struct {
		label, query string
		want         bool
	}{
		{"Project notes", "PROJECT notes", true},
		{"Project notes", "notes project", true},
		{"Project notes", "project missing", false},
		{"Road-map notes", "road_map", true},
		{"Road map", "roadmap", true},
		{"Café notes", "CAFÉ", true},
		{"Project notes", " \t ", true},
	} {
		if got := searchMatcher(tc.query)(tc.label); got != tc.want {
			t.Errorf("searchMatcher(%q)(%q) = %v, want %v", tc.query, tc.label, got, tc.want)
		}
	}
}

func TestPaletteSearchOrderTrashAndStaleResults(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pinned := notion.Page{ID: "pinned", Title: "Project pinned"}
	recent := notion.Page{ID: "recent", Title: "Project recent"}
	other := notion.Page{ID: "other", Title: "Project other"}
	deleted := notion.Page{ID: "deleted", Title: "Project deleted", InTrash: true}
	stalePin := deleted
	stalePin.InTrash = false
	state := store.State{Pins: []notion.Page{pinned, stalePin}, Recents: []notion.Page{recent, pinned}, Pages: []notion.Page{pinned, recent, other, deleted}}
	a := newApp(notion.NewDemo(nil), s, state, nil, "", false)
	defer a.cancel()
	p := &commandMenu{input: tview.NewInputField().SetText("project"), list: tview.NewList(), remoteQuery: "project", remote: []notion.Page{{ID: "remote", Title: "Project remote"}, pinned}}
	labels := func() []string {
		var out []string
		for _, item := range p.items {
			out = append(out, item.label)
		}
		return out
	}
	a.filterPalette(p)
	if got, want := labels(), []string{"Project remote", "Project pinned", "Project recent", "Project other"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("results = %v, want %v", got, want)
	}
	p.remoteQuery = "old query"
	a.filterPalette(p)
	if got, want := labels(), []string{"Project pinned", "Project recent", "Project other", "Search Notion for “project”"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("results after query change = %v, want %v", got, want)
	}
}
