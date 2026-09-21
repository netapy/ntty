package main

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func TestNotionObjectLabelsPreserveSource(t *testing.T) {
	for _, tc := range []struct{ raw, label, link string }{
		{`<mention-date start="2026-09-07"/>`, "@7 Sep 2026", ""},
		{`<mention-date start="2026-09-07" end="2026-09-09"/>`, "@7 Sep 2026 – 9 Sep 2026", ""},
		{`<mention-user id="abc" name="Léa"/>`, "@Léa", ""},
		{`<mention-user url="{{user://abc}}">Ada Lovelace</mention-user>`, "@Ada Lovelace", ""},
		{`<mention-user url="user://abc"/>`, "@Person", ""},
		{`<database url="https://app.notion.com/12345678123442348234123456789abc" inline="false" icon="📬">Tasks &amp; notes</database>`, "▦ Tasks & notes", "database:https://app.notion.com/12345678123442348234123456789abc"},
		{`<page url="https://app.notion.com/p/123">Project</page>`, "↗ Project", "https://app.notion.com/p/123"},
		{`<https://www.example.com/notes/the-next-step?secret=not-in-label>`, "example.com / the next step", "https://www.example.com/notes/the-next-step?secret=not-in-label"},
		{`[https://example.com/notes](https://example.com/notes)`, "example.com / notes", "https://example.com/notes"},
	} {
		t.Run(tc.label, func(t *testing.T) {
			source := "before " + tc.raw + " after\nKeep this next block"
			r := newRichEditor()
			r.SetText(source, false)
			if r.visible != "before "+tc.label+" after\nKeep this next block" {
				t.Fatalf("visible: %q", r.visible)
			}
			if r.GetText() != source {
				t.Fatal("rendering rewrote source")
			}
			r.Replace(0, len("before"), "Changed")
			if r.GetText() != "Changed "+tc.raw+" after\nKeep this next block" {
				t.Fatalf("adjacent edit damaged object: %q", r.GetText())
			}
			r.history(true)
			if r.GetText() != source {
				t.Fatal("undo changed object")
			}
			var opened string
			r.activate = func(link string) { opened = link }
			r.openAt(len("before "))
			if opened != tc.link {
				t.Fatalf("link: %q", opened)
			}
		})
	}
}

func TestCanonicalUserMentionUsesCachedPersonLabel(t *testing.T) {
	r := newRichEditor()
	r.label = func(link string) string {
		if link == "user://abc" {
			return "Ada Lovelace"
		}
		return ""
	}
	source := `Hello <mention-user url="user://abc"/>`
	r.SetText(source, false)
	if r.visible != "Hello @Ada Lovelace" || r.GetText() != source {
		t.Fatalf("canonical mention: visible=%q source=%q", r.visible, r.GetText())
	}
}

func TestSingleClickLinkAndDragSelection(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(60, 8)
	r := newRichEditor()
	r.SetRect(0, 0, 60, 8)
	r.SetText("Read [the article](https://example.com/article) today", false)
	r.Draw(screen)
	_, _, linkStyle, _ := screen.GetContent(7, 0)
	if linkStyle != linkStyle.Underline(true) {
		t.Fatal("link's terminal underline style was not set")
	}
	var opened []string
	r.activate = func(link string) { opened = append(opened, link) }
	mouse := r.MouseHandler()
	focus := func(tview.Primitive) {}
	mouse(tview.MouseLeftDown, tcell.NewEventMouse(7, 0, tcell.Button1, 0), focus)
	mouse(tview.MouseLeftUp, tcell.NewEventMouse(7, 0, tcell.ButtonNone, 0), focus)
	if len(opened) != 1 || opened[0] != "https://example.com/article" {
		t.Fatalf("click: %v", opened)
	}
	r.clicks = 0
	r.lastClick = r.lastClick.Add(-1e9)
	mouse(tview.MouseLeftDown, tcell.NewEventMouse(6, 0, tcell.Button1, 0), focus)
	mouse(tview.MouseMove, tcell.NewEventMouse(13, 0, tcell.Button1, 0), focus)
	mouse(tview.MouseLeftUp, tcell.NewEventMouse(13, 0, tcell.ButtonNone, 0), focus)
	if len(opened) != 1 {
		t.Fatal("dragging a link opened it")
	}
	selected, _, _ := r.GetSelection()
	if strings.TrimSpace(selected) == "" {
		t.Fatal("link text was not selectable")
	}
}

func TestWrappedNotionReferenceHitTesting(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(38, 12)
	r := newRichEditor()
	r.SetRect(0, 0, 38, 12)
	source := `# Planning <mention-date start="2026-09-07"/> with Alex` + "\n\n" + `<database url="https://app.notion.com/12345678123442348234123456789abc">Tasks</database>`
	r.SetText(source, false)
	r.Draw(screen)
	for y := 0; y < 12; y++ {
		for x := 0; x < 38; x++ {
			ch, _, _, _ := screen.GetContent(x, y)
			if ch == '<' || ch == '>' {
				t.Fatalf("raw tag on screen at %d,%d", x, y)
			}
		}
	}
	row, col := r.position(strings.Index(r.visible, "Tasks"))
	var opened string
	r.activate = func(link string) { opened = link }
	mouse := r.MouseHandler()
	focus := func(tview.Primitive) {}
	mouse(tview.MouseLeftDown, tcell.NewEventMouse(col, row, tcell.Button1, 0), focus)
	mouse(tview.MouseLeftUp, tcell.NewEventMouse(col, row, tcell.ButtonNone, 0), focus)
	if opened != "database:https://app.notion.com/12345678123442348234123456789abc" {
		t.Fatalf("wrong wrapped reference: %q", opened)
	}
	if r.GetText() != source {
		t.Fatal("click changed source")
	}
}

func FuzzNotionVisualSourcePreservation(f *testing.F) {
	f.Add("Tasks & notes", "2026-09-07")
	f.Fuzz(func(t *testing.T, title, date string) {
		if !utf8.ValidString(title+date) || strings.ContainsAny(title+date, "<>\n\r\"&") {
			t.Skip()
		}
		source := `<database url="https://example.com/abc">` + title + `</database>` + "\n" + `<mention-date start="` + date + `"/>`
		for _, line := range strings.Split(source, "\n") {
			if raw, _, _ := notionInline(line); raw == "" {
				t.Skip()
			}
		} // Only valid XML objects; malformed Markdown is rejected by the save guard.
		lines := parseRich(source, nil)
		if richMarkdown(lines) != source {
			t.Fatal("source changed by rendering")
		}
		for i := range lines {
			serializeLine(&lines[i])
		}
		if richMarkdown(lines) != source {
			t.Fatalf("serialization changed opaque source: %q", richMarkdown(lines))
		}
	})
}
