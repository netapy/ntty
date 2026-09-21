package main

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"strings"
	"testing"
)

func TestNumberedIndentRestartsAtEachDepth(t *testing.T) {
	r := newRichEditor()
	r.SetText("1. first\n2. second\n3. child\n4. grandchild", false)
	start := strings.Index(r.visible, "3. child")
	r.Select(start, len(r.visible))
	r.InputHandler()(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone), func(tview.Primitive) {})
	start = strings.Index(r.visible, "2. grandchild")
	if start < 0 {
		t.Fatalf("child siblings not renumbered: %q", r.GetText())
	}
	r.Select(start, start)
	r.InputHandler()(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone), func(tview.Primitive) {})
	want := "1. first\n2. second\n\t1. child\n\t\t1. grandchild"
	if r.GetText() != want {
		t.Fatalf("nested list\ngot %q\nwant %q", r.GetText(), want)
	}
	r.Select(len(r.visible), len(r.visible))
	r.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), func(tview.Primitive) {})
	if !strings.HasSuffix(r.GetText(), "\t\t2. ") {
		t.Fatalf("nested Enter: %q", r.GetText())
	}
	r.InputHandler()(tcell.NewEventKey(tcell.KeyBacktab, 0, tcell.ModShift), func(tview.Primitive) {})
	if !strings.HasSuffix(r.GetText(), "\t2. ") {
		t.Fatalf("outdent: %q", r.GetText())
	}
}
