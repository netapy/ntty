package main

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"ntty/internal/notion"
	"ntty/internal/store"
)

func TestBreadcrumbDirectClickAndNarrowLayout(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := newApp(notion.NewDemo(nil), s, store.State{}, nil, "", false)
	defer a.cancel()
	root := notion.Page{ID: "root", Title: "Engineering", Kind: "page"}
	child := notion.Page{ID: "child", Title: "Conformity / CISO", Kind: "page"}
	a.docs[root.ID] = &notion.Doc{Page: root, Text: "Draft retained", Dirty: true}
	a.active = child.ID
	a.resolvedPaths = map[string][]notion.Page{child.ID: {root, child}}
	a.setBreadcrumb(child)
	a.breadcrumb.SetRect(3, 2, 80, 1)
	a.renderBreadcrumb(80)
	link := a.breadcrumbLinks[0]
	hit := hoverAt(a.breadcrumb, 3+link.start, 2)
	if hit.x != 3+link.start || hit.width != link.end-link.start {
		t.Fatalf("hover escaped breadcrumb: %+v", hit)
	}
	if hoverAt(a.breadcrumb, 3+link.end+1, 2).width != 0 {
		t.Fatal("separator is hoverable")
	}
	a.breadcrumbMouse(tview.MouseLeftClick, tcell.NewEventMouse(3+link.start, 2, tcell.Button1, 0))
	if a.active != root.ID || a.modal || a.editor.GetText() != "Draft retained" {
		t.Fatal("breadcrumb did not directly navigate with draft intact")
	}
	a.breadcrumbPages = []notion.Page{{ID: "old", Title: "Very long workspace ancestor"}, root, child}
	a.renderBreadcrumb(40)
	if len(a.breadcrumbLinks) != 3 || a.breadcrumbLinks[1].page.ID != root.ID || !strings.HasPrefix(a.breadcrumb.GetText(false), "  …") {
		t.Fatal("narrow path lost parent navigation")
	}
	if tview.TaggedStringWidth(a.breadcrumb.GetText(false)) > 40 {
		t.Fatal("breadcrumb exceeds available width")
	}
}

func TestBreadcrumbRedrawHasNoRepeatedCharacters(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := newApp(notion.NewDemo(nil), s, store.State{}, nil, "", false)
	defer a.cancel()
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(160, 10)
	a.breadcrumbPages = []notion.Page{{ID: "root", Title: "Acme"}, {ID: "parent", Title: "Progress"}, {ID: "page", Title: "Contracts"}}
	for _, width := range []int{100, 38, 80, 20, 5, 1, 100} {
		a.breadcrumb.SetRect(0, 0, width, 1)
		a.breadcrumb.Draw(screen)
		var line strings.Builder
		for x := 0; x < width; x++ {
			main, combining, _, _ := screen.GetContent(x, 0)
			line.WriteRune(main)
			line.WriteString(string(combining))
		}
		want := a.breadcrumb.GetText(true)
		if strings.TrimRight(line.String(), " ") != strings.TrimRight(want, " ") {
			t.Fatalf("width %d: painted %q; wanted %q", width, line.String(), want)
		}
	}
}

func TestBreadcrumbCollapsesOnlyDuplicateContainers(t *testing.T) {
	database := notion.Page{ID: "db", Title: "Progress", Kind: "database"}
	source := notion.Page{ID: "source", Title: "Progress", Kind: "data_source", ParentID: database.ID}
	page := notion.Page{ID: "page", Title: "Progress", Kind: "page", ParentID: source.ID}
	chain := compactBreadcrumb([]notion.Page{database, source, page, page})
	if len(chain) != 2 || chain[0].ID != source.ID || chain[1].ID != page.ID {
		t.Fatalf("wrong path: %+v", chain)
	}
}

func TestResizeDoesNotRebuildWorkspaceOrReloadDocument(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b := &startupBackend{Backend: notion.NewDemo(nil), calls: make(chan string, 10)}
	a := newApp(b, s, largeWorkspace(), nil, "", false)
	defer a.cancel()
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	a.editor.SetText("Unsaved text stays here", false)
	a.editor.Select(7, 11)
	first := &a.visible[0]
	for _, width := range []int{120, 70, 160, 85, 120} {
		screen.SetSize(width, 36)
		a.ui.GetBeforeDrawFunc()(screen)
		a.layers.SetRect(0, 0, width, 36)
		a.layers.Draw(screen)
		if &a.visible[0] != first {
			t.Fatal("resize rebuilt workspace data")
		}
		if a.editor.GetText() != "Unsaved text stays here" {
			t.Fatal("resize replaced document")
		}
		_, start, end := a.editor.GetSelection()
		if start != 7 || end != 11 {
			t.Fatalf("resize moved selection: %d %d", start, end)
		}
	}
	select {
	case call := <-b.calls:
		t.Fatalf("resize requested %s", call)
	default:
	}
}
