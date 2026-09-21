package main

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"strings"
	"testing"
)

func TestDeleteInlineMentionsAsSingleItems(t *testing.T) {
	for _, mention := range []string{
		`<mention-page url="https://www.notion.so/12345678-1234-4234-8234-123456789abc">Release notes (A&amp;B)</mention-page>`,
		`<mention-user url="user://12345678-1234-1234-1234-123456789abc">Alex</mention-user>`,
		`<mention-date start="2026-09-21"/>`,
	} {
		for _, key := range []tcell.Key{tcell.KeyBackspace2, tcell.KeyDelete} {
			r := newRichEditor()
			source := "Before " + mention + " after"
			r.SetText(source, false)
			at := len("Before ")
			if key == tcell.KeyBackspace2 {
				at = strings.Index(r.visible, " after")
			}
			r.Select(at, at)
			r.InputHandler()(tcell.NewEventKey(key, 0, 0), func(tview.Primitive) {})
			if r.GetText() != "Before  after" {
				t.Fatalf("mention deletion refused: %s => %s", mention, r.GetText())
			}
			r.history(true)
			if r.GetText() != source {
				t.Fatal("undo did not restore exact mention")
			}
			r.Select(0, len(r.visible))
			r.InputHandler()(tcell.NewEventKey(tcell.KeyBackspace2, 0, 0), func(tview.Primitive) {})
			if strings.Contains(r.GetText(), "<mention-") {
				t.Fatal("sentence selection retained mention")
			}
		}
	}
}
