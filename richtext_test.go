package main

import (
	"math/rand"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/rivo/uniseg"
	"ntty/internal/notion"
)

const enhancedMarkdown = "# Héllo **bold** and *italic*\n\n<span underline=\"true\" discussion-urls=\"discussion://one\">underlined</span> <mention-page url=\"https://www.notion.so/12345678123442348234123456789abc\"/>\n<callout icon=\"💡\">Keep <br> metadata</callout>\n<empty-block/>\n- [x] café\n"

func TestRichRoundTripAndSafeEditing(t *testing.T) {
	lines := parseRich(enhancedMarkdown, func(string) string { return "Full article title" })
	if got := richMarkdown(lines); got != enhancedMarkdown {
		t.Fatalf("untouched source changed:\n%s", got)
	}
	display := richDisplay(lines)
	for _, want := range []string{"Héllo bold and italic", "underlined ↗ Full article title", "▎ Note", "☑ café"} {
		if !strings.Contains(display, want) {
			t.Errorf("display missing %q:\n%s", want, display)
		}
	}
	r := newRichEditor()
	r.label = func(string) string { return "Full article title" }
	r.SetText(enhancedMarkdown, false)
	pos := strings.Index(r.visible, "Héllo") + len("Héllo")
	r.Replace(pos, pos, " 世界")
	if !strings.Contains(r.GetText(), "# Héllo 世界 **bold**") {
		t.Fatalf("plain edit lost formatting: %s", r.GetText())
	}
	if !strings.Contains(r.GetText(), `<callout icon="💡">`) || !strings.Contains(r.GetText(), `discussion-urls="discussion://one"`) {
		t.Fatal("editing one line changed untouched enhanced metadata")
	}
	if richDisplay(parseRich(r.GetText(), r.label)) != r.visible {
		t.Fatal("saved source does not reproduce the visible document")
	}

	mention := strings.Index(r.visible, "Full article")
	before := r.GetText()
	r.Replace(mention+1, mention+2, "x")
	if r.GetText() != before {
		t.Fatal("partial atom edit was not rejected")
	}
}

func TestNewEmptyLinesUseNotionEmptyBlocks(t *testing.T) {
	r := newRichEditor()
	r.SetText("Hello", true)
	r.Replace(len(r.visible), len(r.visible), "\n")
	if got := r.GetText(); got != "Hello\n<empty-block/>" {
		t.Fatalf("new blank block: %q", got)
	}
	r.Replace(len(r.visible), len(r.visible), "Next")
	if got := r.GetText(); got != "Hello\nNext" {
		t.Fatalf("typing into blank block: %q", got)
	}
	r.Replace(len(r.visible), len(r.visible), "\n\nLast")
	if got := r.GetText(); got != "Hello\nNext\n<empty-block/>\nLast" {
		t.Fatalf("blank block between paragraphs: %q", got)
	}

	code := newRichEditor()
	code.SetText("```text\nline\n```", false)
	code.Replace(strings.Index(code.visible, "line")+len("line"), strings.Index(code.visible, "line")+len("line"), "\n")
	if strings.Contains(code.GetText(), "<empty-block/>") {
		t.Fatalf("blank code line became a Notion block: %q", code.GetText())
	}
}

func TestBlockActionsAreUndoableAndProtectNotionObjects(t *testing.T) {
	r := newRichEditor()
	r.SetText("One\nTwo\nThree", false)
	r.Select(len("One\nT"), len("One\nT"))
	if !r.BlockAction("move-up") || r.GetText() != "Two\nOne\nThree" {
		t.Fatalf("move block up: %q", r.GetText())
	}
	r.history(true)
	if r.GetText() != "One\nTwo\nThree" {
		t.Fatalf("undo block move: %q", r.GetText())
	}
	r.Select(len("One\nTwo"), len("One\nTwo"))
	if !r.BlockAction("duplicate") || r.GetText() != "One\nTwo\nTwo\nThree" {
		t.Fatalf("duplicate block: %q", r.GetText())
	}
	if !r.BlockAction("delete") || r.GetText() != "One\nTwo\nThree" {
		t.Fatalf("delete duplicated block: %q", r.GetText())
	}

	const protected = "Before\n<unknown url=\"p\" alt=\"embed\"/>\nAfter"
	r.SetText(protected, false)
	atom := strings.Index(r.visible, "[Notion block]")
	r.Select(atom, atom)
	if r.BlockAction("delete") || r.BlockAction("move-up") {
		t.Fatal("protected Notion object accepted a destructive block action")
	}
	r.Block("# ")
	if r.GetText() != protected {
		t.Fatalf("protected block was transformed: %q", r.GetText())
	}
}

func TestPageLinkFormattingRoundTrips(t *testing.T) {
	r := newRichEditor()
	r.SetText("Read this", false)
	r.Select(0, len("Read"))
	r.Link("https://www.notion.so/0123456789abcdef0123456789abcdef", "unused")
	want := "[Read](https://www.notion.so/0123456789abcdef0123456789abcdef) this"
	if got := r.GetText(); got != want {
		t.Fatalf("page link source: %q", got)
	}
	if got := richDisplay(parseRich(r.GetText(), nil)); got != "Read this" {
		t.Fatalf("page link display: %q", got)
	}
}

func TestInsertUserAndTodayMentionsAsAtomicMarkdown(t *testing.T) {
	r := newRichEditor()
	r.SetText("Hello  due ", false)
	r.Select(len("Hello "), len("Hello "))
	user := `<mention-user url="{{user://abc}}">Ada Lovelace</mention-user>`
	if !r.InsertAtom(user) {
		t.Fatal("user mention was rejected")
	}
	if got := r.GetText(); got != "Hello "+user+" due " {
		t.Fatalf("user mention source: %q", got)
	}
	if !strings.Contains(r.visible, "@Ada Lovelace") {
		t.Fatalf("user mention display: %q", r.visible)
	}
	r.Select(len(r.visible), len(r.visible))
	date := `<mention-date start="2026-09-21"/>`
	if !r.InsertAtom(date) || !strings.HasSuffix(r.GetText(), date) || !strings.HasSuffix(r.visible, "@21 Sep 2026") {
		t.Fatalf("date mention: source=%q visible=%q", r.GetText(), r.visible)
	}
	before := r.GetText()
	start := strings.Index(r.visible, "Ada")
	r.Replace(start+1, start+2, "x")
	if r.GetText() != before {
		t.Fatal("partial mention edit was not rejected")
	}
}

func TestMeetingNotesAndCitationsRenderAsProtectedObjects(t *testing.T) {
	const source = "Before\n<meeting-notes readOnlyViewMeetingNoteUrl=\"https://app.notion.com/p/page#block\">\n\t**Weekly meeting** <mention-date start=\"2026-06-01\"/>\n\t<summary>\n\t\t- [ ] Action [^https://app.notion.com/p/page#citation]\n\t</summary>\n</meeting-notes>\nAfter"
	lines := parseRich(source, nil)
	if got := richMarkdown(lines); got != source {
		t.Fatal("meeting note source changed")
	}
	display := richDisplay(lines)
	if !strings.Contains(display, "▣ Weekly meeting @1 Jun 2026") || strings.Contains(display, "<meeting-notes") || strings.Contains(display, "citation") {
		t.Fatalf("meeting note was not collapsed cleanly: %q", display)
	}
	if len(lines) != 3 || len(lines[1].chars) == 0 || lines[1].chars[0].atom == nil || lines[1].chars[0].atom.link != "external:https://app.notion.com/p/page#block" {
		t.Fatalf("meeting note link/shape: %+v", lines)
	}
	const tabs = "<tabs>\n\t<tab>\n\t\tNote\n\t\tPrivate content\n\t</tab>\n\t<tab>\n\t\tCommon pages\n\t</tab>\n</tabs>"
	tabLines := parseRich(tabs, nil)
	if richMarkdown(tabLines) != tabs || richDisplay(tabLines) != "▣ Tabs · Note · Common pages" || len(tabLines) != 1 || tabLines[0].chars[0].atom.link != "notion:current" {
		t.Fatalf("tabs rendering changed source: source=%q display=%q", richMarkdown(tabLines), richDisplay(tabLines))
	}

	const citation = "Action [^https://app.notion.com/p/page#citation]"
	citationLines := parseRich(citation, nil)
	if richMarkdown(citationLines) != citation || richDisplay(citationLines) != "Action ⁕" {
		t.Fatalf("citation rendering changed source: source=%q display=%q", richMarkdown(citationLines), richDisplay(citationLines))
	}
	if got := richDisplay(parseRich("- [ ]", nil)); got != "☐ " {
		t.Fatalf("empty task rendering: %q", got)
	}
}

func TestRichMouseWordDragOutsideAndAutoscroll(t *testing.T) {
	r := newRichEditor()
	r.SetRect(0, 0, 18, 6)
	r.SetText("alpha beta gamma\n"+strings.Repeat("long line here\n", 20), false)
	h := r.MouseHandler()
	focus := func(tview.Primitive) {}
	x, y, _, _ := r.GetInnerRect()
	// Two presses select a word; dragging the second press extends by words.
	h(tview.MouseLeftDown, tcell.NewEventMouse(x+7, y, tcell.Button1, tcell.ModNone), focus)
	h(tview.MouseLeftUp, tcell.NewEventMouse(x+7, y, tcell.ButtonNone, tcell.ModNone), focus)
	h(tview.MouseLeftDown, tcell.NewEventMouse(x+7, y, tcell.Button1, tcell.ModNone), focus)
	h(tview.MouseMove, tcell.NewEventMouse(x+14, y, tcell.Button1, tcell.ModNone), focus)
	selected, _, _ := r.GetSelection()
	if !strings.Contains(selected, "beta") || !strings.Contains(selected, "gamma") {
		t.Fatalf("word drag selection: %q", selected)
	}
	// A captured drag keeps scrolling and selecting beyond the widget.
	h(tview.MouseLeftDown, tcell.NewEventMouse(x, y, tcell.Button1, tcell.ModNone), focus)
	h(tview.MouseMove, tcell.NewEventMouse(x+4, y+12, tcell.Button1, tcell.ModNone), focus)
	for i := 0; i < 4; i++ {
		r.tickMouse()
	}
	offset, _ := r.GetOffset()
	if offset == 0 {
		t.Fatal("drag outside did not autoscroll")
	}
	h(tview.MouseLeftUp, tcell.NewEventMouse(x+4, y+12, tcell.ButtonNone, tcell.ModNone), focus)
	if r.drag {
		t.Fatal("release outside left drag capture active")
	}
}

func TestRichFormattingUndoBlocksAndUnicodeLayout(t *testing.T) {
	r := newRichEditor()
	r.SetRect(0, 0, 9, 8)
	r.SetText("A e\u0301界 word\n\n", false)
	start := strings.Index(r.visible, "word")
	r.Select(start, start+4)
	r.Format(tcell.AttrBold)
	if !strings.Contains(r.GetText(), "**word**") {
		t.Fatalf("bold not serialized: %q", r.GetText())
	}
	r.history(true)
	if r.GetText() != "A e\u0301界 word\n\n" {
		t.Fatalf("undo lost source: %q", r.GetText())
	}
	r.Select(len(r.visible), len(r.visible))
	r.CommandBlock("todo")
	if !strings.HasSuffix(r.GetText(), "- [ ] ") || !strings.HasSuffix(r.visible, "☐ ") {
		t.Fatalf("task block mismatch: source=%q visible=%q", r.GetText(), r.visible)
	}
	r.layout()
	if len(r.rows) < 2 {
		t.Fatalf("unicode wrapping produced too few rows: %+v", r.rows)
	}
	for _, row := range r.rows {
		for _, cell := range row.cells {
			if cell.start < 0 || cell.end < cell.start || cell.end > len(r.visible) {
				t.Fatalf("invalid cell boundary: %+v", cell)
			}
		}
	}
}

func TestHeadingMarkersStayInAccentGutter(t *testing.T) {
	r := newRichEditor()
	r.SetRect(0, 0, 24, 6)
	r.SetBorderPadding(0, 0, 7, 1)
	r.SetText("### a heading long enough to wrap\nplain", false)
	s := tcell.NewSimulationScreen("UTF-8")
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	defer s.Fini()
	s.SetSize(24, 6)
	r.Draw(s)
	x, y, _, _ := r.GetInnerRect()
	for i := 0; i < 3; i++ {
		ch, _, style, _ := s.GetContent(x-4+i, y)
		fg, bg, _ := style.Decompose()
		if ch != '#' || fg != tcell.ColorTeal || bg != tcell.ColorDefault {
			t.Fatalf("heading gutter cell %d: %q %v", i, ch, style)
		}
	}
	for row := y + 1; row < y+4; row++ {
		ch, _, _, _ := s.GetContent(x-2, row)
		if ch == '#' {
			t.Fatal("wrapped continuation repeated the heading marker")
		}
	}
}

func TestRichTypingStyleRoundTrips(t *testing.T) {
	r := newRichEditor()
	r.SetText("", false)
	r.Format(tcell.AttrItalic)
	r.InputHandler()(tcell.NewEventKey(tcell.KeyRune, 'é', tcell.ModNone), func(tview.Primitive) {})
	if r.GetText() != "*é*" || r.visible != "é" {
		t.Fatalf("typing style mismatch: source=%q visible=%q", r.GetText(), r.visible)
	}
	if richDisplay(parseRich(r.GetText(), nil)) != r.visible {
		t.Fatal("typing style failed safety round-trip")
	}
	r.history(true)
	if r.GetText() != "" {
		t.Fatalf("undo typing style: %q", r.GetText())
	}
}

func TestEditorNavigationIndentAndMarkdownShortcuts(t *testing.T) {
	key := func(r *richEditor, k tcell.Key, ch rune, mod tcell.ModMask) {
		r.InputHandler()(tcell.NewEventKey(k, ch, mod), func(tview.Primitive) {})
	}
	r := newRichEditor()
	r.SetRect(0, 0, 10, 8)
	r.SetText("café 世界 words\n- one\n  - two", false)
	r.Select(len("café 世界"), len("café 世界"))
	key(r, tcell.KeyLeft, 0, tcell.ModAlt)
	_, _, end := r.GetSelection()
	if end != len("café ") {
		t.Fatalf("unicode word-left landed at %d", end)
	}
	key(r, tcell.KeyRight, 0, tcell.ModAlt|tcell.ModShift)
	if selected, _, _ := r.GetSelection(); selected != "世界" {
		t.Fatalf("shift word selection: %q", selected)
	}

	start := strings.Index(r.visible, "• one")
	r.Select(start+len("• "), len(r.visible)-len("two"))
	key(r, tcell.KeyTab, 0, tcell.ModNone)
	if !strings.Contains(r.GetText(), "\t- one\n\t  - two") {
		t.Fatalf("indent: %q", r.GetText())
	}
	key(r, tcell.KeyBacktab, 0, tcell.ModShift)
	if !strings.Contains(r.GetText(), "- one\n  - two") {
		t.Fatalf("outdent: %q", r.GetText())
	}
	r.history(true)
	if !strings.Contains(r.GetText(), "\t- one\n\t  - two") {
		t.Fatalf("multi-line outdent was not one undo: %q", r.GetText())
	}

	for _, tc := range []struct{ typed, source string }{{"- ", "- "}, {"1) ", "1) "}, {"### ", "### "}, {"> ", "> "}, {"[] ", "- [ ] "}, {"[x] ", "- [x] "}} {
		e := newRichEditor()
		e.SetText("", false)
		for _, ch := range tc.typed {
			key(e, tcell.KeyRune, ch, tcell.ModNone)
		}
		if e.GetText() != tc.source {
			t.Errorf("shortcut %q: %q", tc.typed, e.GetText())
		}
		e.history(true)
		literal := tc.typed
		if strings.HasPrefix(literal, "[") {
			literal = strings.TrimSuffix(literal, " ")
		} // Tasks convert on ], before the optional space.
		if e.visible != literal {
			t.Errorf("undo shortcut %q: visible %q source %q", tc.typed, e.visible, e.GetText())
		}
		e.history(false)
		if e.GetText() != tc.source {
			t.Errorf("redo shortcut %q: %q", tc.typed, e.GetText())
		}
	}
	after := newRichEditor()
	after.SetText("first\n", true)
	for _, ch := range "- " {
		key(after, tcell.KeyRune, ch, tcell.ModNone)
	}
	if after.GetText() != "first\n- " {
		t.Fatalf("shortcut after block: %q", after.GetText())
	}
	key(after, tcell.KeyBackspace2, 0, tcell.ModNone)
	if after.GetText() != "first\n<empty-block/>" {
		t.Fatalf("backspace did not clear empty list: %q", after.GetText())
	}
	after.history(true)
	if after.GetText() != "first\n- " {
		t.Fatalf("undo empty-list backspace: %q", after.GetText())
	}
	mid := newRichEditor()
	mid.SetText("text-", true)
	key(mid, tcell.KeyRune, ' ', tcell.ModNone)
	if mid.GetText() != "text- " {
		t.Fatalf("mid-line marker transformed: %q", mid.GetText())
	}

	code := newRichEditor()
	code.SetText("", false)
	for _, ch := range "```" {
		key(code, tcell.KeyRune, ch, tcell.ModNone)
	}
	key(code, tcell.KeyEnter, 0, tcell.ModNone)
	if code.GetText() != "```\n\n```" {
		t.Fatalf("code fence: %q", code.GetText())
	}
	code.history(true)
	if code.visible != "```" {
		t.Fatalf("code fence undo: visible %q source %q", code.visible, code.GetText())
	}
}

func TestArrowNavigationUsesRichCursorAfterNativeReset(t *testing.T) {
	r := newRichEditor()
	r.SetRect(0, 0, 14, 7)
	r.SetText("alpha bravo charlie\nsecond café line\nthird 👩‍💻 tail", false)
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(14, 7)
	r.Draw(screen)
	key := func(k tcell.Key, mod tcell.ModMask) {
		r.InputHandler()(tcell.NewEventKey(k, 0, mod), func(tview.Primitive) {})
	}

	start := strings.Index(r.visible, "tail") + len("tail")
	r.Select(start, start)
	// SetText is what the input surface does after a committed edit. Its native
	// cursor temporarily returns to zero; arrows must continue from richEditor.head.
	r.TextArea.SetText(r.visible, false)
	key(tcell.KeyLeft, tcell.ModNone)
	if want := previousGrapheme(r.visible, start); r.head != want || r.head == 0 {
		t.Fatalf("Left jumped from %d to %d; want %d", start, r.head, want)
	}

	start = strings.Index(r.visible, "third") + len("third")
	r.Select(start, start)
	beforeRow, _ := r.position(r.head)
	if beforeRow < 2 {
		t.Fatalf("test text did not wrap into enough rows: %d", beforeRow)
	}
	r.TextArea.SetText(r.visible, false)
	key(tcell.KeyUp, tcell.ModNone)
	afterRow, _ := r.position(r.head)
	if afterRow != beforeRow-1 || r.head == 0 {
		t.Fatalf("Up jumped from row %d offset %d to row %d offset %d", beforeRow, start, afterRow, r.head)
	}
}

func TestNotionTableRendersAndCellsRemainEditable(t *testing.T) {
	const source = `<table fit-page-width="true" header-row="true">
	<colgroup>
		<col>
		<col>
	</colgroup>
	<tr>
		<td>Name</td>
		<td>Owner</td>
	</tr>
	<tr>
		<td>Roadmap</td>
		<td>Ada</td>
	</tr>
</table>`
	r := newRichEditor()
	r.SetText(source, false)
	if r.GetText() != source || richMarkdown(parseRich(source, nil)) != source {
		t.Fatal("table source did not round-trip exactly")
	}
	for _, visible := range []string{"Name", "Owner", "Roadmap", "Ada", "┌", "└", "│"} {
		if !strings.Contains(r.visible, visible) {
			t.Fatalf("rendered table is missing %q: %q", visible, r.visible)
		}
	}
	if strings.Contains(r.visible, "<table") || strings.Contains(r.visible, "<td") {
		t.Fatalf("table XML leaked into the editor: %q", r.visible)
	}
	start := strings.Index(r.visible, "Roadmap")
	r.Replace(start, start+len("Roadmap"), "Launch plan")
	if !strings.Contains(r.GetText(), "<td>Launch plan</td>") || strings.Contains(r.GetText(), "Roadmap") {
		t.Fatalf("table cell edit did not preserve its tags: %q", r.GetText())
	}
}

func TestTableSlashCommandCreatesEditableNotionTable(t *testing.T) {
	r := newRichEditor()
	r.SetText("<empty-block/>", false)
	r.CommandBlock("table")
	if !strings.Contains(r.GetText(), `<table fit-page-width="true" header-row="true">`) || !strings.Contains(r.visible, "Column 1") {
		t.Fatalf("table command did not create a rendered table: source=%q visible=%q", r.GetText(), r.visible)
	}
	selected, _, _ := r.GetSelection()
	if selected != "Column 1" {
		t.Fatalf("table command did not select the first header: %q", selected)
	}
	r.InputHandler()(tcell.NewEventKey(tcell.KeyRune, 'N', tcell.ModNone), func(tview.Primitive) {})
	if !strings.Contains(r.GetText(), "<td>N</td>") {
		t.Fatalf("typing into the selected header broke the table: %q", r.GetText())
	}
}

func TestTableColumnsReflowWithoutSavingDecoration(t *testing.T) {
	r := newRichEditor()
	r.SetText("", false)
	r.CommandBlock("table")
	at := strings.Index(r.visible, "Column 1")
	r.Replace(at, at+len("Column 1"), "Longer café heading")
	at = strings.Index(r.visible, "Value 2")
	r.Replace(at, at+len("Value 2"), "Second changed")
	lines := strings.Split(r.visible, "\n")
	width := tview.TaggedStringWidth(lines[0])
	for _, line := range lines {
		if line != "" && tview.TaggedStringWidth(line) != width {
			t.Fatalf("ragged table after editing: %q", r.visible)
		}
	}
	if !strings.Contains(r.GetText(), "<td>Longer café heading</td>") || !strings.Contains(r.GetText(), "<td>Second changed</td>") || strings.ContainsAny(r.GetText(), "┌┬┴│") {
		t.Fatalf("cell editing saved decoration or lost text: %q", r.GetText())
	}
}

func TestDeleteWholeTableBySelectionAndBlockAction(t *testing.T) {
	for _, action := range []string{"selection", "block"} {
		t.Run(action, func(t *testing.T) {
			r := newRichEditor()
			r.SetText("", false)
			r.CommandBlock("table")
			table := r.GetText()
			r.SetText("Before\n"+table+"\nAfter", false)
			if action == "selection" {
				start := strings.Index(r.visible, "┌")
				end := strings.Index(r.visible, "After")
				r.Replace(start, end, "")
			} else {
				at := strings.Index(r.visible, "Value 1")
				r.Select(at, at)
				if !r.BlockAction("delete") {
					t.Fatal("table block deletion refused")
				}
			}
			if r.GetText() != "Before\nAfter" {
				t.Fatalf("table not deleted cleanly: %q", r.GetText())
			}
			r.history(true)
			if r.GetText() != "Before\n"+table+"\nAfter" {
				t.Fatal("undo lost table")
			}
		})
	}
}

func TestTableTypingNeverEscapesCellsOrSplitsRows(t *testing.T) {
	r := newRichEditor()
	r.SetText("", false)
	r.CommandBlock("table")
	at := strings.Index(r.visible, "Column 1") + len("Column 1")
	r.Replace(at, at, " appended")
	if !strings.Contains(r.GetText(), "<td>Column 1 appended</td>") {
		t.Fatalf("typing escaped cell: %s", r.GetText())
	}
	at = strings.Index(r.visible, "Value 2") + len("Value ")
	r.Select(at, at)
	before := r.GetText()
	r.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
	if r.GetText() != before {
		t.Fatal("Enter split table structure")
	}
	// Typing at the end of a row cannot silently disappear into its XML suffix.
	start := strings.Index(r.visible, "Value 1")
	end := start + strings.Index(r.visible[start:], "\n")
	r.Replace(end, end, "must stay visible")
	if strings.Contains(r.GetText(), "must stay visible") && !strings.Contains(r.visible, "must stay visible") {
		t.Fatal("typing disappeared into table structure")
	}
	at = strings.Index(r.visible, "Column 1 appended")
	r.Replace(at, at+len("Column 1 appended"), "")
	if !strings.Contains(r.GetText(), "<td></td>") {
		t.Fatal("clearing cell removed structure or was rejected")
	}
	r.Replace(r.head, r.head, "Refilled")
	if !strings.Contains(r.GetText(), "<td>Refilled</td>") {
		t.Fatalf("empty cell cannot be edited: %s", r.GetText())
	}
}

func TestArrowSelectionDirectionAndGraphemeBoundaries(t *testing.T) {
	r := newRichEditor()
	r.SetRect(0, 0, 30, 5)
	r.SetText("a é 👩‍💻 z", false)
	key := func(k tcell.Key, mod tcell.ModMask) {
		r.InputHandler()(tcell.NewEventKey(k, 0, mod), func(tview.Primitive) {})
	}

	end := strings.Index(r.visible, " z")
	r.Select(end, end)
	want := previousGrapheme(r.visible, end)
	key(tcell.KeyLeft, tcell.ModShift)
	if r.anchor != end || r.head != want {
		t.Fatalf("Shift+Left lost selection direction: anchor=%d head=%d want=%d", r.anchor, r.head, want)
	}
	key(tcell.KeyRight, tcell.ModNone)
	if r.anchor != end || r.head != end {
		t.Fatalf("Right did not collapse selection at its right edge: %d,%d", r.anchor, r.head)
	}

	emojiStart := strings.Index(r.visible, "👩")
	emojiEnd := emojiStart + len("👩‍💻")
	r.Select(emojiEnd, emojiEnd)
	key(tcell.KeyLeft, tcell.ModNone)
	if r.head != emojiStart {
		t.Fatalf("Left split an emoji grapheme: got %d want %d", r.head, emojiStart)
	}
}

func TestVerticalNavigationKeepsSoftWrapRowAffinity(t *testing.T) {
	r := newRichEditor()
	r.SetRect(0, 0, 10, 6)
	r.SetText("abc abcdefghijklmnop", false)
	r.layout()
	if len(r.rows) < 2 || len(r.rows[1].cells) < 6 {
		t.Fatalf("test text did not wrap as expected: %+v", r.rows)
	}
	start := r.rows[1].cells[5].start
	r.Select(start, start)
	key := func(k tcell.Key) {
		r.InputHandler()(tcell.NewEventKey(k, 0, tcell.ModNone), func(tview.Primitive) {})
	}

	key(tcell.KeyUp)
	row, _ := r.cursorPosition()
	if row != 0 {
		t.Fatalf("Up did not land on the short wrapped row: row=%d offset=%d visual=%d", row, r.head, r.visualRow)
	}
	// The same byte offset is also the start of row 1. The explicit affinity is
	// what keeps another Up/Down deterministic at this soft boundary.
	if canonical, _ := r.position(r.head); canonical != 1 {
		t.Fatalf("test did not exercise an ambiguous wrap boundary: canonical row=%d", canonical)
	}
	key(tcell.KeyDown)
	row, _ = r.cursorPosition()
	if row != 1 {
		t.Fatalf("Down lost the remembered visual column: row=%d offset=%d visual=%d", row, r.head, r.visualRow)
	}
	key(tcell.KeyUp)
	key(tcell.KeyEnd)
	key(tcell.KeyHome)
	row, _ = r.cursorPosition()
	if row != 0 || r.head != r.rows[0].start {
		t.Fatalf("Home lost soft-wrap affinity after End: row=%d offset=%d", row, r.head)
	}
}

func TestRichDeletionIsCaretAnchoredAndCannotCrossBlocks(t *testing.T) {
	const source = "# same\n<span discussion-urls=\"discussion://keep\">same</span>\nsame\n- [ ] same\n> same"
	r := newRichEditor()
	r.SetText(source, false)
	key := func(k tcell.Key, ch rune, mod tcell.ModMask) {
		r.InputHandler()(tcell.NewEventKey(k, ch, mod), func(tview.Primitive) {})
	}

	// Repeated visible text used to let the global prefix/suffix diff apply a
	// word deletion to the wrong rich line.
	second := strings.Index(r.visible, "\nsame\n☐") + 1 + len("same")
	r.Select(second, second)
	key(tcell.KeyRune, 'x', tcell.ModNone)
	key(tcell.KeyCtrlW, 0, tcell.ModNone)
	if got, want := r.GetText(), "# same\n<span discussion-urls=\"discussion://keep\">same</span>\n<empty-block/>\n- [ ] same\n> same"; got != want {
		t.Fatalf("word deletion moved across repeated blocks:\n got %q\nwant %q", got, want)
	}

	for _, tc := range []struct {
		name string
		pos  int
		key  tcell.Key
	}{
		{"word delete at block start", len("before\n"), tcell.KeyCtrlW},
	} {
		t.Run(tc.name, func(t *testing.T) {
			editor := newRichEditor()
			editor.SetText("before\ncurrent\nafter", false)
			editor.Select(tc.pos, tc.pos)
			editor.InputHandler()(tcell.NewEventKey(tc.key, 0, tcell.ModNone), func(tview.Primitive) {})
			if got, want := editor.GetText(), "before\ncurrent\nafter"; got != want {
				t.Fatalf("implicit deletion crossed a block boundary:\n got %q\nwant %q", got, want)
			}
		})
	}

	// Crossing a boundary is still possible when the user selected it.
	boundary := strings.Index(r.visible, "☐ same") - 1
	beforeLines := strings.Count(r.visible, "\n")
	r.Select(boundary, boundary+1)
	key(tcell.KeyBackspace2, 0, tcell.ModNone)
	if strings.Count(r.visible, "\n") != beforeLines-1 {
		t.Fatal("explicitly selected newline was not deleted")
	}
}

func TestRichEditorRejectsProtectedObjectDeletion(t *testing.T) {
	const source = "Before\n<span discussion-urls=\"thread\">Discussed</span>\n<unknown id=\"block\"/>\nAfter"
	r := newRichEditor()
	r.SetText(source, false)
	notices := 0
	r.notice = func(string) { notices++ }
	start := strings.Index(r.visible, "Discussed")
	end := strings.Index(r.visible, "After")
	r.Replace(start, end, "")
	if r.GetText() != source || notices != 1 {
		t.Fatalf("protected deletion changed source: %q (notices %d)", r.GetText(), notices)
	}
	r.Replace(0, len("Before"), "Changed")
	if !strings.HasPrefix(r.GetText(), "Changed\n") {
		t.Fatalf("adjacent prose edit was blocked: %q", r.GetText())
	}
}

func TestRichEditorStatefulPlainTextDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(20260920))
	rich := newRichEditor()
	plain := tview.NewTextArea().SetWordWrap(true)
	rich.SetRect(0, 0, 20, 8)
	plain.SetRect(0, 0, 20, 8)
	const initial = "first line\nrepeat repeat\nlast line"
	rich.SetText(initial, false)
	plain.SetText(initial, false)
	focus := func(tview.Primitive) {}
	keys := []struct {
		key  tcell.Key
		rune rune
	}{
		{tcell.KeyRune, 'a'}, {tcell.KeyRune, ' '}, {tcell.KeyRune, 'é'},
		{tcell.KeyBackspace2, 0}, {tcell.KeyBackspace, 0}, {tcell.KeyDelete, 0},
		{tcell.KeyCtrlW, 0}, {tcell.KeyEnter, 0},
	}
	for step := 0; step < 10_000; step++ {
		if step%7 == 0 {
			text := plain.GetText()
			a, b := rng.Intn(len(text)+1), rng.Intn(len(text)+1)
			for a > 0 && a < len(text) && !utf8.RuneStart(text[a]) {
				a--
			}
			for b > 0 && b < len(text) && !utf8.RuneStart(text[b]) {
				b--
			}
			rich.Select(a, b)
			plain.Select(a, b)
		}
		old := plain.GetText()
		_, oldStart, oldEnd := plain.GetSelection()
		key := keys[rng.Intn(len(keys))]
		event := tcell.NewEventKey(key.key, key.rune, tcell.ModNone)
		if oldStart == oldEnd && (key.key == tcell.KeyBackspace || key.key == tcell.KeyBackspace2 || key.key == tcell.KeyDelete) {
			start, end := oldStart, oldEnd
			if key.key == tcell.KeyDelete {
				g := uniseg.NewGraphemes(old[end:])
				if g.Next() {
					_, n := g.Positions()
					end += n
				}
			} else {
				g := uniseg.NewGraphemes(old[:start])
				for g.Next() {
					start, _ = g.Positions()
				}
			}
			plain.Replace(start, end, "")
		} else if key.key == tcell.KeyCtrlW && oldStart != oldEnd {
			plain.Replace(oldStart, oldEnd, "")
		} else {
			plain.InputHandler()(event, focus)
		}
		// Word deletion cannot merge blocks; single Backspace/Delete can remove
		// exactly one newline, as in an ordinary editor.
		if key.key == tcell.KeyCtrlW && oldStart == oldEnd && strings.Count(plain.GetText(), "\n") < strings.Count(old, "\n") {
			plain.SetText(old, false).Select(oldStart, oldEnd)
		}
		rich.InputHandler()(event, focus)
		gotText, wantText := rich.visible, plain.GetText()
		_, gotStart, gotEnd := rich.GetSelection()
		_, wantStart, wantEnd := plain.GetSelection()
		if gotText != wantText || gotStart != wantStart || gotEnd != wantEnd || richDisplay(parseRich(rich.GetText(), nil)) != gotText {
			t.Fatalf("step %d key %v: rich=%q [%d,%d], plain=%q [%d,%d], source=%q", step, key.key, gotText, gotStart, gotEnd, wantText, wantStart, wantEnd, rich.GetText())
		}
		// Keep the oracle as a value model; this test is for ntty's long-lived
		// native engine, not tview's own undo piece-chain implementation.
		plain.SetText(wantText, false).Select(wantStart, wantEnd)
	}
}

func TestOrdinaryEditFastPathMatchesFullValidation(t *testing.T) {
	rng := rand.New(rand.NewSource(20260921))
	source := strings.Repeat("ordinary paragraph text café\n", 4000)
	r := newRichEditor()
	r.SetText(source, false)
	for step := 0; step < 128; step++ {
		line := rng.Intn(len(r.lines))
		start := 0
		for i := 0; i < line; i++ {
			start += len(r.lines[i].display()) + 1
		}
		start += rng.Intn(len(r.lines[line].display()) + 1)
		start = textBoundary(r.visible, start)
		inserted := []string{"x", "é", " ", "*", ""}[rng.Intn(5)]
		end := start
		if inserted == "" && end < lineEnd(r.visible, end) {
			end = nextGrapheme(r.visible, end)
		}

		beforeSource, beforeVisible := r.source, r.visible
		expected, ok := editRichAt(r.lines, beforeVisible, start, end, inserted)
		wantVisible := beforeVisible[:start] + inserted + beforeVisible[end:]
		if !ok || !r.validModel(expected, wantVisible) {
			t.Fatalf("slow semantics rejected ordinary edit %d", step)
		}
		wantSource := richMarkdown(expected)
		if !notion.PreservesProtectedObjects(beforeSource, wantSource) {
			t.Fatalf("slow semantics rejected protected content at edit %d", step)
		}

		r.Replace(start, end, inserted)
		if r.visible != wantVisible || r.source != wantSource {
			t.Fatalf("edit %d differs from full validation", step)
		}
	}

	for _, source := range []string{
		"Before\n<unknown id=\"keep\"/>\nAfter",
		`<mention-date start="2026-09-21"/>`,
		"<table><tr><td>cell</td></tr></table>",
		"![image](https://example.com/image.png)",
		"{color=red}",
	} {
		protected := newRichEditor()
		protected.SetText(source, false)
		if protected.fastEdits {
			t.Fatalf("structured document enabled the fast path: %q", source)
		}
	}
	if !ordinaryRichDocument("Before\n<empty-block/>\nAfter") {
		t.Fatal("ordinary Notion empty paragraph disabled the fast path")
	}
}

func FuzzRichRoundTrip(f *testing.F) {
	for _, seed := range []string{"", enhancedMarkdown, "## title\n- item\n> quote", "`a``b` &amp; <unknown x=\"1\"/>"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, source string) {
		if len(source) > 16<<10 || !strings.Contains(source, "\n") && len(source) > 2<<10 {
			t.Skip()
		}
		lines := parseRich(source, nil)
		if got := richMarkdown(lines); got != source {
			t.Fatalf("untouched round trip changed source")
		}
		_ = richDisplay(lines)
	})
}

func BenchmarkParseRichArticle(b *testing.B) {
	source := strings.Repeat(enhancedMarkdown, 60)
	b.ReportAllocs()
	for b.Loop() {
		parseRich(source, nil)
	}
}

func BenchmarkRichEditorType100KB(b *testing.B) {
	source := strings.Repeat("ordinary paragraph text that stays plain\n", 2500)
	for _, benchmark := range []struct {
		name string
		slow bool
	}{{"FastPath", false}, {"FullValidation", true}} {
		b.Run(benchmark.name, func(b *testing.B) {
			r := newRichEditor()
			r.SetText(source, false)
			pos := len("ordinary paragraph")
			b.ReportAllocs()
			for b.Loop() {
				if benchmark.slow {
					r.fastEdits = false
				}
				r.Replace(pos, pos, "x")
				if benchmark.slow {
					r.fastEdits = false
				}
				r.Replace(pos, pos+1, "")
			}
		})
	}
}

func TestArticleFixtureIntegrity(t *testing.T) {
	path := os.Getenv("NTTY_ARTICLE_FIXTURE")
	if path == "" {
		t.Skip("set NTTY_ARTICLE_FIXTURE to exercise a read-only Notion export")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)
	if got := richMarkdown(parseRich(source, nil)); got != source {
		t.Fatal("parser changed untouched article source")
	}
	r := newRichEditor()
	r.SetText(source, false)
	step := max(1, len(r.visible)/128)
	checked := 0
	for pos := 0; pos <= len(r.visible) && checked < 128; pos += step {
		for pos < len(r.visible) && !utf8.RuneStart(r.visible[pos]) {
			pos++
		}
		if c := r.charAt(pos); c != nil && c.atom != nil {
			continue
		}
		r.Replace(pos, pos, "x")
		if richDisplay(parseRich(r.GetText(), nil)) != r.visible {
			t.Fatalf("edit %d cannot round-trip", checked)
		}
		r.history(true)
		if r.GetText() != source {
			t.Fatalf("undo %d did not restore exact source", checked)
		}
		checked++
	}
	if checked < 32 {
		t.Fatalf("only exercised %d article positions", checked)
	}
}
