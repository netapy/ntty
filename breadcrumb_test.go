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
	root := notion.Page{ID: "root", Title: "Lunar Studio", Kind: "page"}
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
