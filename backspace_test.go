package main

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func TestBackspaceFirstCharacterAndLineStart(t *testing.T) {
	for _, tc := range []struct{ name, source, before, want string }{
		{"first character", "abc", "a", "bc"},
		{"first character next line", "before\nabc\nafter", "before\na", "before\nbc\nafter"},
		{"accent", "before\nébc\nafter", "before\né", "before\nbc\nafter"},
		{"combining character", "before\ne\u0301bc\nafter", "before\ne\u0301", "before\nbc\nafter"},
		{"heading", "# abc\nafter", "a", "# bc\nafter"},
		{"bullet", "- abc\nafter", "• a", "- bc\nafter"},
		{"sole heading character", "# a\nafter", "a", "# \nafter"},
		{"join paragraphs", "before\nabc\nafter", "before\n", "beforeabc\nafter"},
		{"remove empty line", "before\n\nabc\nafter", "before\n\n", "before\nabc\nafter"},
		{"remove leading empty line", "\nabc\nafter", "\n", "abc\nafter"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRichEditor()
			r.SetText(tc.source, false)
			if !strings.HasPrefix(r.visible, tc.before) {
				t.Fatalf("bad caret fixture %q in %q", tc.before, r.visible)
			}
			r.Select(len(tc.before), len(tc.before))
			r.InputHandler()(tcell.NewEventKey(tcell.KeyBackspace2, 0, tcell.ModNone), func(tview.Primitive) {})
			if got := r.GetText(); got != tc.want {
				t.Fatalf("backspace: got %q, want %q", got, tc.want)
			}
			r.history(true)
			if got := r.GetText(); got != tc.source {
				t.Fatalf("undo changed neighbors: %q", got)
			}
		})
	}
}

func TestDeleteJoinsOnlyOneBoundary(t *testing.T) {
	r := newRichEditor()
	r.SetText("same\nsame\nsame", false)
	r.Select(4, 4)
	r.InputHandler()(tcell.NewEventKey(tcell.KeyDelete, 0, tcell.ModNone), func(tview.Primitive) {})
	if r.GetText() != "samesame\nsame" {
		t.Fatalf("wrong join: %q", r.GetText())
	}
	r.history(true)
	if r.GetText() != "same\nsame\nsame" {
		t.Fatalf("wrong undo: %q", r.GetText())
	}
}

func TestCommandBackspaceTerminalEncodings(t *testing.T) {
	for _, event := range []*tcell.EventKey{
		tcell.NewEventKey(tcell.KeyCtrlU, 0, tcell.ModNone), // Ghostty super+backspace = \x15.
		tcell.NewEventKey(tcell.KeyBackspace2, 0, tcell.ModMeta),
	} {
		for _, tc := range []struct{ source, caret, want string }{
			{"before\ncafé words suffix\nafter", "before\ncafé words", "before\n suffix\nafter"},
			{"- café words\nafter", "• café words", "- \nafter"},
			{"# café words\nafter", "café words", "# \nafter"},
			{"before\nwords", "before\n", "before\nwords"},
		} {
			r := newRichEditor()
			r.SetText(tc.source, false)
			r.Select(len(tc.caret), len(tc.caret))
			r.InputHandler()(event, func(tview.Primitive) {})
			if r.GetText() != tc.want {
				t.Fatalf("key %v: got %q want %q", event, r.GetText(), tc.want)
			}
			if tc.want != tc.source {
				r.history(true)
				if r.GetText() != tc.source {
					t.Fatal("line deletion undo lost content")
				}
			}
		}
	}
}

func TestChecklistClosingBracketShortcut(t *testing.T) {
	for _, keys := range []string{"[]", "[ ]", "[x]", "- [ ]", "[] "} {
		r := newRichEditor()
		r.SetText("before\n\nafter", false)
		r.Select(len("before\n"), len("before\n"))
		for _, key := range keys {
			r.InputHandler()(tcell.NewEventKey(tcell.KeyRune, key, tcell.ModNone), func(tview.Primitive) {})
		}
		want := "before\n- [ ] \nafter"
		if keys == "[x]" {
			want = "before\n- [x] \nafter"
		}
		if r.GetText() != want {
			t.Fatalf("shortcut %q: got %q want %q", keys, r.GetText(), want)
		}
		r.InputHandler()(tcell.NewEventKey(tcell.KeyRune, 'a', tcell.ModNone), func(tview.Primitive) {})
		if !strings.Contains(r.GetText(), "] a\nafter") {
			t.Fatalf("typing after shortcut: %q", r.GetText())
		}
	}
	r := newRichEditor()
	r.SetText("Use ", true)
	for _, key := range "[]" {
		r.InputHandler()(tcell.NewEventKey(tcell.KeyRune, key, tcell.ModNone), func(tview.Primitive) {})
	}
	if r.visible != "Use []" {
		t.Fatalf("midline brackets transformed: %q", r.visible)
	}
}
