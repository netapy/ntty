package main

import (
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/rivo/uniseg"
	"ntty/internal/notion"
)

type richSnapshot struct {
	text                      string
	anchor, head, row, visual int
}

var (
	numberedBlock = regexp.MustCompile(`^(\s*)(\d+)([.)] )`)
	spaceBlock    = regexp.MustCompile(`^(?:#{1,6}|[-+*]|\d+[.)]|>|\[\]|\[[xX]\]) $`)
	taskShortcut  = regexp.MustCompile(`^(\s*)(?:[-+*] )?\[([ xX]?)\]$`)
)

// TextArea owns the plain-text editing engine. The surrounding model retains
// Notion source/annotations and supplies formatting, semantic undo and hit tests.
type richEditor struct {
	*tview.TextArea
	lines                                []richLine
	visible, source                      string
	changed                              func()
	label                                func(string) string
	activate                             func(string)
	menu                                 func()
	slash                                func()
	mention                              func()
	notice                               func(string)
	rows                                 []richRow
	layoutWidth                          int
	fastEdits                            bool
	anchor, head                         int
	undo, redo                           []richSnapshot
	lastEdit                             time.Time
	lastHead                             int
	goalColumn                           int
	visualRow                            int
	typing                               *tcell.AttrMask
	drag, scrollDrag                     bool
	mouseX, mouseY, downX, downY, clicks int
	lastClick                            time.Time
	wordStart, wordEnd                   int
}

func newRichEditor() *richEditor {
	r := &richEditor{TextArea: tview.NewTextArea(), goalColumn: -1, visualRow: -1}
	r.SetWordWrap(true)
	r.SetText("", false)
	return r
}
func (r *richEditor) SetChangedFunc(fn func()) *richEditor { r.changed = fn; return r }
func (r *richEditor) GetText() string                      { return r.source }
func (r *richEditor) SetText(text string, end bool) *richEditor {
	r.lines = parseRich(text, r.label)
	r.source = text
	r.visible = richDisplay(r.lines)
	r.fastEdits = ordinaryRichDocument(text)
	r.rows = nil
	r.TextArea.SetText(r.visible, end)
	r.anchor, r.head = 0, 0
	if end {
		r.anchor = len(r.visible)
		r.head = r.anchor
	}
	r.undo, r.redo = nil, nil
	r.typing = nil
	r.drag = false
	r.clicks = 0
	r.lastEdit = time.Time{}
	r.goalColumn = -1
	r.visualRow = -1
	if r.changed != nil {
		r.changed()
	}
	return r
}

// Rebase accepts server-normalized Markdown without throwing away local history
// when its visible document is unchanged.
func (r *richEditor) Rebase(text string) bool {
	lines := parseRich(text, r.label)
	if richDisplay(lines) != r.visible {
		return false
	}
	r.lines, r.source = lines, text
	r.fastEdits = ordinaryRichDocument(text)
	r.rows = nil
	return true
}
func (r *richEditor) Select(start, end int) *richEditor {
	r.anchor, r.head = textBoundary(r.visible, start), textBoundary(r.visible, end)
	r.goalColumn = -1
	r.visualRow = -1
	r.syncNativeSelection()
	return r
}

func (r *richEditor) GetSelection() (string, int, int) {
	start, end := min(r.anchor, r.head), max(r.anchor, r.head)
	return r.visible[start:end], start, end
}

func textBoundary(text string, offset int) int {
	offset = min(max(0, offset), len(text))
	for offset > 0 && offset < len(text) && !utf8.RuneStart(text[offset]) {
		offset--
	}
	return offset
}

func mapTextOffset(oldText, newText string, offset int) int {
	offset = textBoundary(oldText, offset)
	prefix := 0
	for prefix < len(oldText) && prefix < len(newText) && oldText[prefix] == newText[prefix] {
		prefix++
	}
	for prefix > 0 && (prefix < len(oldText) && !utf8.RuneStart(oldText[prefix]) || prefix < len(newText) && !utf8.RuneStart(newText[prefix])) {
		prefix--
	}
	suffix := 0
	for suffix < len(oldText)-prefix && suffix < len(newText)-prefix && oldText[len(oldText)-1-suffix] == newText[len(newText)-1-suffix] {
		suffix++
	}
	if offset <= prefix {
		return textBoundary(newText, offset)
	}
	if offset >= len(oldText)-suffix {
		return textBoundary(newText, len(newText)-(len(oldText)-offset))
	}
	line, column := linePoint(oldText, offset)
	start := 0
	for i := 0; i < line && start < len(newText); i++ {
		next := strings.IndexByte(newText[start:], '\n')
		if next < 0 {
			start = len(newText)
			break
		}
		start += next + 1
	}
	end := lineEnd(newText, start)
	pos := start
	for column > 0 && pos < end {
		_, size := utf8.DecodeRuneInString(newText[pos:end])
		pos += size
		column--
	}
	return textBoundary(newText, pos)
}

func (r *richEditor) syncNativeSelection() {
	start, end := min(r.anchor, r.head), max(r.anchor, r.head)
	r.TextArea.Select(start, end)
}
func (r *richEditor) snapshot() richSnapshot {
	row, _ := r.GetOffset()
	return richSnapshot{text: r.source, anchor: r.anchor, head: r.head, row: row, visual: r.visualRow}
}
func (r *richEditor) restore(s richSnapshot) {
	r.lines = parseRich(s.text, r.label)
	r.source = s.text
	r.visible = richDisplay(r.lines)
	r.fastEdits = ordinaryRichDocument(s.text)
	r.rows = nil
	r.TextArea.SetText(r.visible, false)
	r.Select(s.anchor, s.head)
	r.visualRow = s.visual
	r.SetOffset(s.row, 0)
	r.typing = nil
	r.lastEdit = time.Time{}
	if r.changed != nil {
		r.changed()
	}
}
func (r *richEditor) record(before richSnapshot, group bool) {
	if r.source == before.text {
		return
	}
	if !group || time.Since(r.lastEdit) > 700*time.Millisecond || before.head != r.lastHead {
		r.undo = append(r.undo, before)
		if len(r.undo) > 100 {
			r.undo = r.undo[len(r.undo)-100:]
		}
	}
	r.redo = nil
	r.lastEdit = time.Now()
	r.lastHead = r.head
	if r.changed != nil {
		r.changed()
	}
}
func (r *richEditor) history(back bool) {
	from, to := &r.undo, &r.redo
	if !back {
		from, to = &r.redo, &r.undo
	}
	if len(*from) == 0 {
		return
	}
	s := (*from)[len(*from)-1]
	*from = (*from)[:len(*from)-1]
	*to = append(*to, r.snapshot())
	r.restore(s)
	r.ensureCursor()
}
func (r *richEditor) ensureCursor() {
	row, _ := r.cursorPosition()
	_, _, _, height := r.GetInnerRect()
	offset, _ := r.GetOffset()
	if row < offset {
		offset = row
	} else if row >= offset+height {
		offset = max(0, row-height+1)
	}
	r.SetOffset(offset, 0)
}
func (r *richEditor) updateModel() {
	r.source = richMarkdown(r.lines)
	r.visible = richDisplay(r.lines)
	r.fastEdits = ordinaryRichDocument(r.source)
	r.rows = nil
	r.visualRow = -1
	row, _ := r.GetOffset()
	r.TextArea.SetText(r.visible, false)
	r.syncNativeSelection()
	r.SetOffset(row, 0)
}

func (r *richEditor) validModel(lines []richLine, visible string) bool {
	markdown := richMarkdown(lines)
	parsed := parseRich(markdown, r.label)
	return richDisplay(parsed) == visible || strings.Contains(markdown, "<table") && sameRichContent(lines, parsed)
}

// fastValidEdit proves that a single ordinary line still has exactly the
// display produced by the full parser. Documents containing protected objects,
// structural edits and styled/atomic lines keep the full-document checks.
func (r *richEditor) fastValidEdit(before, after []richLine, old, visible string, start, end int, inserted string) bool {
	if !r.fastEdits || r.typing != nil || strings.Contains(inserted, "\n") || len(before) != len(after) {
		return false
	}
	from, _ := linePoint(old, start)
	to, _ := linePoint(old, end)
	if from != to || from >= len(before) || !ordinaryRichLine(before[from]) || !ordinaryRichLine(after[from]) {
		return false
	}
	parsed := parseRich(after[from].raw, r.label)
	lineStart := lineStart(visible, start)
	lineEnd := lineEnd(visible, lineStart)
	return len(parsed) == 1 && richDisplay(parsed) == visible[lineStart:lineEnd] && ordinaryRichDocument(after[from].raw)
}

// The preservation scanner's protected forms all start with one of these
// tokens. Stay deliberately conservative; empty-block is ordinary paragraph
// storage and is the only Notion tag admitted to the fast path.
func ordinaryRichDocument(source string) bool {
	source = strings.ReplaceAll(strings.ReplaceAll(source, "<empty-block/>", ""), "<empty-block />", "")
	return !strings.ContainsAny(source, "<{") && !strings.Contains(source, "![")
}

func ordinaryRichLine(line richLine) bool {
	if line.prefix != "" || line.visual != "" || line.heading != 0 || line.literal || strings.Contains(line.raw, "\n") {
		return false
	}
	for _, c := range line.chars {
		if c.atom != nil || c.marks != 0 || len(c.wrappers) != 0 || c.link != "" || c.code {
			return false
		}
	}
	return true
}

// Table borders/padding may reflow after typing; all text and source atoms
// must still match. Decoration is never saved as cell content.
func sameRichContent(a, b []richLine) bool {
	signature := func(lines []richLine) string {
		var out strings.Builder
		for _, line := range lines {
			out.WriteString("V" + strconv.Quote(line.visual))
			var previous *richAtom
			for _, c := range line.chars {
				if c.atom == nil {
					out.WriteString("T" + strconv.QuoteRune(c.r))
				} else if c.atom != previous {
					out.WriteString("A" + strconv.Quote(c.atom.raw))
				}
				previous = c.atom
			}
			out.WriteByte('\n')
		}
		return out.String()
	}
	return signature(a) == signature(b)
}

// nativeEdit identifies the one contiguous edit made by TextArea using the
// selection and caret that existed before it ran. It deliberately does not
// search the whole document: repeated text must never move an edit to another
// Notion block.
func nativeEdit(old, text string, before richSnapshot, newHead int) (start, end int, inserted string, ok bool) {
	start, end = min(before.anchor, before.head), max(before.anchor, before.head)
	if start != end {
		if newHead >= start && newHead <= len(text) && old[:start]+text[start:newHead]+old[end:] == text {
			return start, end, text[start:newHead], true
		}
		return 0, 0, "", false
	}
	caret := before.head
	switch {
	case newHead >= caret && newHead <= len(text) && old[:caret]+text[caret:newHead]+old[caret:] == text:
		return caret, caret, text[caret:newHead], true
	case newHead < caret && old[:newHead]+old[caret:] == text:
		return newHead, caret, "", true
	case newHead == caret && len(text) < len(old):
		end = caret + len(old) - len(text)
		if end <= len(old) && old[:caret]+old[end:] == text {
			return caret, end, "", true
		}
	}
	return 0, 0, "", false
}

func (r *richEditor) transact(fn func(), group bool) {
	before := r.snapshot()
	old := r.visible
	r.syncNativeSelection()
	fn()
	text := r.TextArea.GetText()
	if text == old {
		// Replacing a selection with identical text still collapses the native
		// caret. Mirror that state even though the document itself did not change.
		_, start, end := r.TextArea.GetSelection()
		if start == end {
			r.anchor, r.head = end, end
		}
		r.ensureCursor()
		return
	}
	_, _, newHead := r.TextArea.GetSelection()
	start, end, inserted, ok := nativeEdit(old, text, before, newHead)
	// An unselected destructive key must not silently merge adjacent Notion
	// blocks. Selecting the newline explicitly remains available.
	if ok && before.anchor == before.head && inserted == "" && strings.Contains(old[start:end], "\n") {
		ok = false
	}
	var lines []richLine
	if ok {
		lines, ok = editRichAt(r.lines, old, start, end, inserted)
	}
	fast := ok && r.fastValidEdit(r.lines, lines, old, text, start, end, inserted)
	source := ""
	if ok {
		source = richMarkdown(lines)
	}
	if !ok || !fast && (!r.validModel(lines, text) || !notion.PreservesProtectedObjects(r.source, source)) {
		r.TextArea.SetText(old, false)
		r.Select(before.anchor, before.head)
		r.SetOffset(before.row, 0)
		if r.notice != nil {
			r.notice("That edit would remove protected Notion content; edit it in Notion")
		}
		return
	}
	r.lines = lines
	r.visible = text
	r.source = source
	if !fast {
		r.fastEdits = ordinaryRichDocument(source)
	}
	r.rows = nil
	_, _, r.head = r.TextArea.GetSelection()
	r.anchor = r.head
	r.goalColumn = -1
	r.visualRow = -1
	if r.typing != nil {
		// Apply the explicitly chosen typing style only to inserted characters.
		start, end := min(before.anchor, before.head), r.head
		r.markRange(start, end, *r.typing, true)
		r.source = richMarkdown(r.lines)
		if !r.validModel(r.lines, r.visible) {
			r.restore(before)
			if r.notice != nil {
				r.notice("That edit cannot be represented safely")
			}
			return
		}
	}
	// TextArea's long-lived edit-piece chain can corrupt later backspaces after
	// SetText/undo/selection changes. Our model owns undo, so keep TextArea as a
	// clean rendering/input surface after every committed edit.
	if strings.Contains(r.source, "<table") {
		old := r.visible
		r.lines = parseRich(r.source, r.label)
		r.visible = richDisplay(r.lines)
		r.head = mapTextOffset(old, r.visible, r.head)
		r.anchor = r.head
	}
	if !fast {
		r.TextArea.SetText(r.visible, false)
		r.syncNativeSelection()
	}
	r.record(before, group)
	r.ensureCursor()
}
func (r *richEditor) Replace(start, end int, text string) *richEditor {
	r.Select(start, end)
	r.transact(func() { r.TextArea.Replace(start, end, text) }, false)
	return r
}

func (r *richEditor) InputHandler() func(*tcell.EventKey, func(tview.Primitive)) {
	return r.WrapInputHandler(func(e *tcell.EventKey, focus func(tview.Primitive)) {
		if r.GetDisabled() {
			return
		}
		r.clicks = 0
		r.drag = false
		native := r.TextArea.InputHandler()
		delegate := func(p tview.Primitive) {
			if p == r.TextArea {
				p = r
			}
			focus(p)
		}
		mods := e.Modifiers()
		meta := mods&tcell.ModMeta != 0
		if e.Key() == tcell.KeyRune && meta {
			switch e.Rune() {
			case 'a':
				r.Select(0, len(r.visible))
				return
			case 'c':
				e = tcell.NewEventKey(tcell.KeyCtrlQ, 0, tcell.ModNone)
			case 'x':
				e = tcell.NewEventKey(tcell.KeyCtrlX, 0, tcell.ModNone)
			case 'v':
				e = tcell.NewEventKey(tcell.KeyCtrlV, 0, tcell.ModNone)
			case 'z':
				r.history(mods&tcell.ModShift == 0)
				return
			}
		}
		switch e.Key() {
		case tcell.KeyCtrlZ:
			r.history(e.Modifiers()&tcell.ModShift == 0)
			return
		case tcell.KeyCtrlY:
			r.history(false)
			return
		case tcell.KeyCtrlB:
			r.Format(tcell.AttrBold)
			return
		case tcell.KeyCtrlU:
			r.deleteToLineStart()
			return
		case tcell.KeyCtrlW:
			if r.anchor != r.head {
				r.Replace(min(r.anchor, r.head), max(r.anchor, r.head), "")
				return
			}
		case tcell.KeyCtrlA, tcell.KeyCtrlL:
			r.Select(0, len(r.visible))
			return
		case tcell.KeyTab:
			if r.navigateTableCell(false) {
				return
			}
			r.indent(false)
			return
		case tcell.KeyBacktab:
			if r.navigateTableCell(true) {
				return
			}
			r.indent(true)
			return
		case tcell.KeyEnter:
			if e.Modifiers() == 0 && r.navigateTableCell(false) {
				return
			}
			if e.Modifiers()&tcell.ModCtrl != 0 {
				r.openAt(r.head)
				return
			}
			if r.codeFence() || r.enter() {
				return
			}
		case tcell.KeyBackspace, tcell.KeyBackspace2:
			line, col := linePoint(r.visible, r.head)
			if mods == 0 && r.anchor == r.head && r.lines[line].prefix != "" && col <= utf8.RuneCountInString(r.lines[line].visual) {
				r.Block("")
				return
			}
			if mods == 0 {
				r.deleteCharacter(false)
				return
			}
		case tcell.KeyDelete:
			if mods == 0 {
				r.deleteCharacter(true)
				return
			}
		}
		if (e.Key() == tcell.KeyBackspace || e.Key() == tcell.KeyBackspace2 || e.Key() == tcell.KeyDelete) && r.anchor == r.head && mods&(tcell.ModCtrl|tcell.ModAlt|tcell.ModMeta) != 0 {
			start, end := r.head, r.head
			if e.Key() == tcell.KeyDelete {
				end = r.wordMove(r.head, true)
			} else if meta {
				r.deleteToLineStart()
				return
			} else {
				start = r.wordMove(r.head, false)
			}
			if start != end {
				r.Replace(start, end, "")
			}
			return
		}
		if e.Key() == tcell.KeyRune && e.Modifiers()&tcell.ModAlt != 0 {
			switch e.Rune() {
			case 'u':
				r.Format(tcell.AttrUnderline)
				return
			case 'i':
				r.Format(tcell.AttrItalic)
				return
			case 's':
				r.Format(tcell.AttrStrikeThrough)
				return
			case 'c':
				r.Code()
				return
			}
		}
		if e.Key() == tcell.KeyRune && e.Modifiers() == 0 {
			if (e.Rune() == ' ' || e.Rune() == ']') && r.blockShortcut(string(e.Rune())) {
				return
			}
			if e.Rune() == ' ' && r.anchor == r.head {
				line, _ := linePoint(r.visible, r.head)
				if len(r.lines[line].chars) == 0 && strings.Contains(r.lines[line].prefix, "[") {
					return
				}
			}
		}
		if e.Key() == tcell.KeyRune && e.Rune() == '/' && r.anchor == r.head {
			line, _ := linePoint(r.visible, r.head)
			if strings.TrimSpace(r.lines[line].display()) == "" && r.slash != nil {
				r.slash()
				return
			}
		}
		if e.Key() == tcell.KeyRune && e.Rune() == '@' && e.Modifiers() == 0 && r.anchor == r.head && r.mention != nil {
			r.mention()
			return
		}
		navigation := e.Key() == tcell.KeyLeft || e.Key() == tcell.KeyRight || e.Key() == tcell.KeyUp || e.Key() == tcell.KeyDown || e.Key() == tcell.KeyHome || e.Key() == tcell.KeyEnd || e.Key() == tcell.KeyPgUp || e.Key() == tcell.KeyPgDn
		if navigation {
			r.navigate(e)
			return
		}
		group := e.Key() == tcell.KeyRune && e.Modifiers() == 0 || e.Key() == tcell.KeyBackspace2
		r.transact(func() { native(e, delegate) }, group)
		if e.Key() == tcell.KeyRune {
			r.shortcut()
		}
	})
}

// Delete one displayed character, not a native screen-coordinate range. This
// also makes line joining exactly one newline, including at wrapped edges.
func (r *richEditor) deleteCharacter(forward bool) {
	start, end := min(r.anchor, r.head), max(r.anchor, r.head)
	if start == end {
		if forward {
			g := uniseg.NewGraphemes(r.visible[end:])
			if g.Next() {
				_, n := g.Positions()
				end += n
			}
		} else {
			g := uniseg.NewGraphemes(r.visible[:start])
			for g.Next() {
				start, _ = g.Positions()
			}
		}
	}
	if start != end {
		// Inline mentions behave as one editable reference, not a collection
		// of letters in the display label. Never expand into structural atoms.
		offset := 0
		for _, line := range r.lines {
			pos := offset + len(line.visual)
			for i := 0; i < len(line.chars); {
				c := line.chars[i]
				atomStart := pos
				pos += len(string(c.r))
				i++
				if c.atom == nil {
					continue
				}
				for i < len(line.chars) && line.chars[i].atom == c.atom {
					pos += len(string(line.chars[i].r))
					i++
				}
				if (startsTag(c.atom.raw, "mention-page") || startsTag(c.atom.raw, "mention-user") || startsTag(c.atom.raw, "mention-date")) && start < pos && end > atomStart {
					start = min(start, atomStart)
					end = max(end, pos)
				}
			}
			offset += len(line.display()) + 1
		}
		r.Replace(start, end, "")
	}
}

func literalShortcut(s string) string {
	if len(s) > 0 && s[0] >= '0' && s[0] <= '9' {
		if i := strings.IndexAny(s, ".)"); i >= 0 {
			return s[:i] + "\\" + s[i:]
		}
	}
	return "\\" + s
}

func (r *richEditor) blockShortcut(suffix string) bool {
	if r.anchor != r.head {
		return false
	}
	line, _ := linePoint(r.visible, r.head)
	l := r.lines[line]
	if l.literal || r.head != lineEnd(r.visible, r.head) {
		return false
	}
	for _, c := range l.chars {
		if c.atom != nil || c.marks != 0 || len(c.wrappers) != 0 || c.code {
			return false
		}
	}
	candidate := l.prefix + charText(l.chars) + suffix
	shortcut := candidate
	if m := taskShortcut.FindStringSubmatch(strings.TrimSuffix(candidate, " ")); m != nil {
		check := m[2]
		if check == "" {
			check = " "
		}
		shortcut = m[1] + "- [" + strings.ToLower(check) + "] "
	} else if !spaceBlock.MatchString(candidate) {
		return false
	}
	parsed := parseRich(shortcut, r.label)[0]
	before := r.snapshot()
	raw := strings.Split(before.text, "\n")
	raw[line] = literalShortcut(candidate)
	before.text = strings.Join(raw, "\n")
	before.anchor, before.head = r.head+1, r.head+1
	start := lineStart(r.visible, r.head)
	r.lines[line] = parsed
	r.anchor, r.head = start+len(parsed.display()), start+len(parsed.display())
	r.updateModel()
	r.record(before, false)
	r.ensureCursor()
	return true
}

func (r *richEditor) deleteToLineStart() {
	start, end := min(r.anchor, r.head), max(r.anchor, r.head)
	if start == end {
		line, _ := linePoint(r.visible, r.head)
		start = min(end, lineStart(r.visible, r.head)+len(r.lines[line].visual))
	}
	if start != end {
		r.Replace(start, end, "")
	}
}

func lineStart(text string, pos int) int {
	return strings.LastIndex(text[:min(pos, len(text))], "\n") + 1
}
func lineEnd(text string, pos int) int {
	if i := strings.Index(text[min(pos, len(text)):], "\n"); i >= 0 {
		return min(pos, len(text)) + i
	}
	return len(text)
}
func wordClass(r rune) int {
	if unicode.IsSpace(r) {
		return 0
	}
	if unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsMark(r) || r == '_' {
		return 1
	}
	return 2
}
func (r *richEditor) wordMove(pos int, forward bool) int {
	start, end := lineStart(r.visible, pos), lineEnd(r.visible, pos)
	if forward {
		for pos < end {
			ch, n := utf8.DecodeRuneInString(r.visible[pos:end])
			if wordClass(ch) != 0 {
				break
			}
			pos += n
		}
		if pos < end {
			ch, n := utf8.DecodeRuneInString(r.visible[pos:end])
			class := wordClass(ch)
			pos += n
			for pos < end {
				ch, n = utf8.DecodeRuneInString(r.visible[pos:end])
				if wordClass(ch) != class {
					break
				}
				pos += n
			}
		}
		return pos
	}
	for pos > start {
		ch, n := utf8.DecodeLastRuneInString(r.visible[start:pos])
		if wordClass(ch) != 0 {
			break
		}
		pos -= n
	}
	if pos > start {
		ch, n := utf8.DecodeLastRuneInString(r.visible[start:pos])
		class := wordClass(ch)
		pos -= n
		for pos > start {
			ch, n = utf8.DecodeLastRuneInString(r.visible[start:pos])
			if wordClass(ch) != class {
				break
			}
			pos -= n
		}
	}
	return pos
}

func previousGrapheme(text string, pos int) int {
	pos = textBoundary(text, pos)
	start := pos
	graphemes := uniseg.NewGraphemes(text[:pos])
	for graphemes.Next() {
		start, _ = graphemes.Positions()
	}
	return start
}

func nextGrapheme(text string, pos int) int {
	pos = textBoundary(text, pos)
	graphemes := uniseg.NewGraphemes(text[pos:])
	if graphemes.Next() {
		_, end := graphemes.Positions()
		return pos + end
	}
	return pos
}

func (r *richEditor) rowEnd(row int) int {
	if row < 0 || row >= len(r.rows) {
		return r.head
	}
	end := r.rows[row].end
	if end > r.rows[row].start && end <= len(r.visible) && r.visible[end-1] == '\n' {
		end--
	}
	return end
}

func (r *richEditor) offsetAtColumn(row, column int) int {
	if row < 0 || row >= len(r.rows) {
		return r.head
	}
	line := r.rows[row]
	for _, cell := range line.cells {
		if cell.start < len(r.visible) && r.visible[cell.start] == '\n' {
			return cell.start
		}
		if column < cell.x+max(1, cell.width) {
			return cell.start
		}
	}
	return r.rowEnd(row)
}

func (r *richEditor) verticalPosition(delta int) (offset, goal, row int) {
	r.layout()
	if len(r.rows) == 0 {
		return r.head, -1, -1
	}
	row, column := r.cursorPosition()
	row = min(max(0, row), len(r.rows)-1)
	goal = r.goalColumn
	if goal < 0 {
		goal = column
	}
	row = min(max(0, row+delta), len(r.rows)-1)
	return r.offsetAtColumn(row, goal), goal, row
}

// Navigation is owned by the rich model. TextArea remains the input/rendering
// surface, but its lazily-computed wrapped cursor can be reset by SetText or a
// resize. Keeping the byte offset here (plus row affinity only at ambiguous
// soft-wrap boundaries) prevents an arrow key from ever teleporting to offset
// zero because native row state was stale.
func (r *richEditor) navigate(event *tcell.EventKey) {
	r.typing = nil
	r.lastEdit = time.Time{}
	mods := event.Modifiers()
	shift := mods&tcell.ModShift != 0
	key := event.Key()
	if mods&tcell.ModAlt != 0 && mods&(tcell.ModMeta|tcell.ModCtrl) == 0 && (key == tcell.KeyUp || key == tcell.KeyDown) {
		offset, _ := r.GetOffset()
		if key == tcell.KeyUp {
			offset--
		} else {
			offset++
		}
		_, _, _, height := r.GetInnerRect()
		r.layout()
		r.SetOffset(min(max(0, offset), max(0, len(r.rows)-height)), 0)
		return
	}

	if !shift && r.anchor != r.head && (key == tcell.KeyLeft || key == tcell.KeyRight) && mods&(tcell.ModMeta|tcell.ModCtrl|tcell.ModAlt) == 0 {
		if key == tcell.KeyLeft {
			r.head = min(r.anchor, r.head)
		} else {
			r.head = max(r.anchor, r.head)
		}
		r.anchor = r.head
		r.goalColumn = -1
		r.visualRow = -1
		r.syncNativeSelection()
		r.ensureCursor()
		return
	}

	pos, vertical, visualRow := r.head, false, -1
	switch {
	case mods&tcell.ModMeta != 0 && (key == tcell.KeyUp || key == tcell.KeyDown):
		if key == tcell.KeyUp {
			pos = 0
		} else {
			pos = len(r.visible)
		}
	case mods&tcell.ModMeta != 0 && (key == tcell.KeyLeft || key == tcell.KeyRight):
		if key == tcell.KeyLeft {
			pos = lineStart(r.visible, r.head)
		} else {
			pos = lineEnd(r.visible, r.head)
		}
	case mods&tcell.ModCtrl != 0 && (key == tcell.KeyHome || key == tcell.KeyEnd):
		if key == tcell.KeyHome {
			pos = 0
		} else {
			pos = len(r.visible)
		}
	case mods&(tcell.ModCtrl|tcell.ModAlt) != 0 && (key == tcell.KeyLeft || key == tcell.KeyRight):
		pos = r.wordMove(r.head, key == tcell.KeyRight)
	case key == tcell.KeyLeft:
		pos = previousGrapheme(r.visible, r.head)
	case key == tcell.KeyRight:
		pos = nextGrapheme(r.visible, r.head)
	case key == tcell.KeyUp || key == tcell.KeyDown:
		delta := -1
		if key == tcell.KeyDown {
			delta = 1
		}
		pos, r.goalColumn, visualRow = r.verticalPosition(delta)
		vertical = true
	case key == tcell.KeyPgUp || key == tcell.KeyPgDn:
		_, _, _, height := r.GetInnerRect()
		delta := -max(1, height)
		if key == tcell.KeyPgDn {
			delta = max(1, height)
		}
		pos, r.goalColumn, visualRow = r.verticalPosition(delta)
		vertical = true
	case key == tcell.KeyHome || key == tcell.KeyEnd:
		r.layout()
		row, _ := r.cursorPosition()
		row = min(max(0, row), max(0, len(r.rows)-1))
		if len(r.rows) > 0 {
			if key == tcell.KeyHome {
				pos = r.rows[row].start
			} else {
				pos = r.rowEnd(row)
			}
			visualRow = row
		}
	}
	pos = textBoundary(r.visible, pos)
	if shift {
		r.head = pos
	} else {
		r.anchor, r.head = pos, pos
	}
	if !vertical {
		r.goalColumn = -1
	}
	r.visualRow = visualRow
	r.syncNativeSelection()
	r.ensureCursor()
}

func (r *richEditor) indent(out bool) {
	if r.GetDisabled() {
		return
	}
	before := r.snapshot()
	first, _ := linePoint(r.visible, min(r.anchor, r.head))
	last, _ := linePoint(r.visible, max(r.anchor, r.head))
	if r.anchor != r.head && max(r.anchor, r.head) > 0 && r.visible[max(r.anchor, r.head)-1] == '\n' {
		last--
	}
	raw := make([]string, len(r.lines))
	for i, line := range r.lines {
		raw[i] = line.raw
	}
	if first < 0 || last >= len(raw) {
		return
	}
	for i := first; i <= last; i++ {
		if richLineProtected(r.lines[i]) {
			if r.notice != nil {
				r.notice("That block contains protected Notion content")
			}
			return
		}
	}
	for i := first; i <= last; i++ {
		if out {
			n := 0
			if strings.HasPrefix(raw[i], "\t") {
				n = 1
			} else {
				for n < 2 && n < len(raw[i]) && raw[i][n] == ' ' {
					n++
				}
			}
			raw[i] = raw[i][n:]
		} else {
			raw[i] = "\t" + raw[i]
		}
	}
	next := strings.Join(raw, "\n")
	if next == before.text {
		return
	}
	lines := parseRich(next, r.label)
	renumberLists(lines)
	next = richMarkdown(lines)
	if richMarkdown(lines) != next || !notion.PreservesProtectedObjects(before.text, next) {
		if r.notice != nil {
			r.notice("That block contains protected Notion content")
		}
		return
	}
	delta := make([]int, len(raw))
	for i := range lines {
		delta[i] = len(lines[i].display()) - len(r.lines[i].display())
	}
	mapPos := func(pos int) int {
		line, col := linePoint(r.visible, pos)
		shift := 0
		for i := 0; i < line; i++ {
			shift += delta[i]
		}
		if col > 0 || before.anchor == before.head && delta[line] > 0 {
			shift += delta[line]
		}
		return max(0, pos+shift)
	}
	r.lines, r.source, r.visible = lines, next, richDisplay(lines)
	r.anchor, r.head = mapPos(before.anchor), mapPos(before.head)
	r.updateModel()
	r.record(before, false)
	r.ensureCursor()
}
func (r *richEditor) PasteHandler() func(string, func(tview.Primitive)) {
	return r.WrapPasteHandler(func(text string, focus func(tview.Primitive)) {
		if r.GetDisabled() {
			return
		}
		r.transact(func() { r.TextArea.PasteHandler()(text, focus) }, false)
	})
}

func (r *richEditor) markRange(start, end int, marks tcell.AttrMask, replace bool) {
	pos := 0
	for li := range r.lines {
		l := &r.lines[li]
		pos += len(l.visual)
		dirty := false
		for i := range l.chars {
			c := &l.chars[i]
			n := utf8.RuneLen(c.r)
			if pos >= start && pos < end && c.atom == nil {
				if replace {
					c.marks = marks
				} else {
					c.marks ^= marks
				}
				dirty = true
			}
			pos += n
		}
		if dirty {
			serializeLine(l)
		}
		pos++
	}
}
func (r *richEditor) Format(mark tcell.AttrMask) {
	if r.GetDisabled() {
		return
	}
	if r.anchor == r.head {
		style := tcell.AttrMask(0)
		if c := r.charAt(r.head); c != nil {
			style = c.marks
		}
		if r.typing != nil {
			style = *r.typing
		}
		style ^= mark
		r.typing = &style
		if r.notice != nil {
			r.notice("Formatting applies to the next text you type")
		}
		return
	}
	before := r.snapshot()
	start, end := min(r.anchor, r.head), max(r.anchor, r.head)
	all := true
	pos := 0
	for _, l := range r.lines {
		pos += len(l.visual)
		for _, c := range l.chars {
			if pos >= start && pos < end && c.atom == nil && c.marks&mark == 0 {
				all = false
			}
			pos += utf8.RuneLen(c.r)
		}
		pos++
	}
	pos = 0
	for li := range r.lines {
		l := &r.lines[li]
		pos += len(l.visual)
		dirty := false
		for i := range l.chars {
			c := &l.chars[i]
			if pos >= start && pos < end && c.atom == nil {
				if all {
					c.marks &^= mark
				} else {
					c.marks |= mark
				}
				if mark == tcell.AttrUnderline {
					c.wrappers = append([]richWrapper(nil), c.wrappers...)
					for j := range c.wrappers {
						c.wrappers[j].open = strings.ReplaceAll(c.wrappers[j].open, ` underline="true"`, "")
					}
				}
				dirty = true
			}
			pos += utf8.RuneLen(c.r)
		}
		if dirty {
			serializeLine(l)
		}
		pos++
	}
	if !r.validModel(r.lines, r.visible) {
		r.restore(before)
		if r.notice != nil {
			r.notice("That formatting cannot be represented safely")
		}
		return
	}
	r.updateModel()
	r.record(before, false)
}

func (r *richEditor) Link(url, fallback string) {
	if r.GetDisabled() || url == "" {
		return
	}
	if r.anchor == r.head {
		start := r.head
		r.Replace(start, start, fallback)
		r.Select(start, start+len(fallback))
	}
	before := r.snapshot()
	start, end := min(r.anchor, r.head), max(r.anchor, r.head)
	link := richWrapper{"[", "](" + url + ")"}
	pos := 0
	for li := range r.lines {
		l := &r.lines[li]
		pos += len(l.visual)
		dirty := false
		for i := range l.chars {
			c := &l.chars[i]
			if pos >= start && pos < end && c.atom == nil {
				c.wrappers = append(append([]richWrapper{}, c.wrappers...), link)
				c.link = url
				dirty = true
			}
			pos += utf8.RuneLen(c.r)
		}
		if dirty {
			serializeLine(l)
		}
		pos++
	}
	if !r.validModel(r.lines, r.visible) {
		r.restore(before)
		return
	}
	r.updateModel()
	r.record(before, false)
}

func (r *richEditor) InsertAtom(raw string) bool {
	if r.GetDisabled() || r.anchor != r.head {
		return false
	}
	parsedRaw, label, link := notionInline(raw)
	if parsedRaw != raw || label == "" {
		return false
	}
	before := r.snapshot()
	line, column := linePoint(r.visible, r.head)
	if line < 0 || line >= len(r.lines) {
		return false
	}
	item := &r.lines[line]
	column -= utf8.RuneCountInString(item.visual)
	if column < 0 || column > len(item.chars) {
		return false
	}
	if column > 0 && column < len(item.chars) && item.chars[column].atom != nil && item.chars[column-1].atom == item.chars[column].atom {
		return false
	}
	inserted := atomChars(raw, label, link)
	tail := append([]richChar(nil), item.chars[column:]...)
	item.chars = append(item.chars[:column], inserted...)
	item.chars = append(item.chars, tail...)
	serializeLine(item)
	nextVisible := richDisplay(r.lines)
	if !r.validModel(r.lines, nextVisible) || !notion.PreservesProtectedObjects(before.text, richMarkdown(r.lines)) {
		r.restore(before)
		return false
	}
	r.anchor, r.head = r.head+len(label), r.head+len(label)
	r.updateModel()
	r.record(before, false)
	r.ensureCursor()
	return true
}

func (r *richEditor) Code() {
	if r.GetDisabled() || r.anchor == r.head {
		return
	}
	before := r.snapshot()
	pos := 0
	start, end := min(r.anchor, r.head), max(r.anchor, r.head)
	for li := range r.lines {
		l := &r.lines[li]
		pos += len(l.visual)
		dirty := false
		for i := range l.chars {
			c := &l.chars[i]
			if pos >= start && pos < end && c.atom == nil {
				c.code = !c.code
				dirty = true
			}
			pos += utf8.RuneLen(c.r)
		}
		if dirty {
			serializeLine(l)
		}
		pos++
	}
	if !r.validModel(r.lines, r.visible) {
		r.restore(before)
		return
	}
	r.updateModel()
	r.record(before, false)
}
func (r *richEditor) Block(prefix string) {
	if r.GetDisabled() {
		return
	}
	before := r.snapshot()
	line, col := linePoint(r.visible, r.head)
	l := &r.lines[line]
	if richLineProtected(*l) {
		if r.notice != nil {
			r.notice("That block contains protected Notion content")
		}
		return
	}
	oldVisual := l.visual
	l.prefix = prefix
	l.heading = 0
	l.visual = ""
	if prefix != "" {
		parsed := parseRich(prefix+"x", nil)[0]
		l.visual = parsed.visual
		l.heading = parsed.heading
	}
	serializeLine(l)
	_ = col
	r.head = max(0, r.head-len(oldVisual)+len(l.visual))
	r.anchor = r.head
	if !r.validModel(r.lines, richDisplay(r.lines)) || !notion.PreservesProtectedObjects(before.text, richMarkdown(r.lines)) {
		r.restore(before)
		if r.notice != nil {
			r.notice("That block contains protected Notion content")
		}
		return
	}
	r.updateModel()
	r.record(before, false)
	r.ensureCursor()
}

func (r *richEditor) CommandBlock(kind string) {
	prefixes := map[string]string{"paragraph": "", "heading1": "# ", "heading2": "## ", "heading3": "### ", "heading4": "#### ", "bullet": "- ", "numbered": "1. ", "todo": "- [ ] ", "quote": "> "}
	if prefix, ok := prefixes[kind]; ok {
		r.Block(prefix)
		return
	}
	if r.GetDisabled() || r.anchor != r.head {
		return
	}
	line, _ := linePoint(r.visible, r.head)
	if strings.TrimSpace(r.lines[line].display()) != "" {
		return
	}
	before := r.snapshot()
	selectionStart, selectionEnd := 0, 0
	switch kind {
	case "code":
		replacement := parseRich("```\n\n```", r.label)
		r.lines = append(append(append([]richLine{}, r.lines[:line]...), replacement...), r.lines[line+1:]...)
	case "table":
		const template = `<table fit-page-width="true" header-row="true">
	<colgroup>
		<col>
		<col>
	</colgroup>
	<tr>
		<td>Column 1</td>
		<td>Column 2</td>
	</tr>
	<tr>
		<td>Value 1</td>
		<td>Value 2</td>
	</tr>
</table>`
		replacement := parseRich(template, r.label)
		r.lines = append(append(append([]richLine{}, r.lines[:line]...), replacement...), r.lines[line+1:]...)
	case "divider":
		r.lines[line] = parseRich("---", r.label)[0]
	default:
		return
	}
	r.source = richMarkdown(r.lines)
	r.visible = richDisplay(r.lines)
	pos := 0
	for i := 0; i < line; i++ {
		pos += len(r.lines[i].display()) + 1
	}
	if kind == "code" {
		pos += len(r.lines[line].display()) + 1
	} else if kind == "table" {
		table := richDisplay(r.lines[line:])
		if cell := strings.Index(table, "Column 1"); cell >= 0 {
			selectionStart, selectionEnd = pos+cell, pos+cell+len("Column 1")
		}
	} else {
		pos += len(r.lines[line].display())
	}
	if selectionEnd > selectionStart {
		r.anchor, r.head = selectionStart, selectionEnd
	} else {
		r.anchor, r.head = pos, pos
	}
	if !r.validModel(r.lines, r.visible) {
		r.restore(before)
		return
	}
	r.updateModel()
	r.record(before, false)
	r.ensureCursor()
}

// BlockAction changes one ordinary Markdown block while treating enhanced
// Notion objects as indivisible. It returns false when the action cannot be
// represented losslessly (for example, moving a protected embed).
func (r *richEditor) BlockAction(action string) bool {
	if r.GetDisabled() || r.anchor != r.head || len(r.lines) == 0 {
		return false
	}
	before := r.snapshot()
	line, column := linePoint(r.visible, r.head)
	if line < 0 || line >= len(r.lines) {
		return false
	}
	next := append([]richLine(nil), r.lines...)
	target := line
	switch action {
	case "move-up":
		if line == 0 || richLineProtected(next[line]) || richLineProtected(next[line-1]) {
			return false
		}
		next[line-1], next[line] = next[line], next[line-1]
		target = line - 1
	case "move-down":
		if line+1 >= len(next) || richLineProtected(next[line]) || richLineProtected(next[line+1]) {
			return false
		}
		next[line], next[line+1] = next[line+1], next[line]
		target = line + 1
	case "duplicate":
		clone := parseRich(r.lines[line].raw, r.label)
		if len(clone) != 1 {
			return false
		}
		next = append(next, richLine{})
		copy(next[line+2:], next[line+1:])
		next[line+1] = clone[0]
		target = line + 1
	case "delete":
		first, last := line, line
		for i := line; i >= 0; i-- {
			if startsTag(next[i].raw, "table") && !next[i].literal {
				for j := i; j < len(next); j++ {
					if strings.HasSuffix(strings.TrimSpace(next[j].raw), "</table>") {
						if j >= line {
							first, last = i, j
						}
						break
					}
				}
				break
			}
		}
		if last-first+1 == len(next) {
			next = []richLine{{}}
			target, column = 0, 0
		} else {
			next = append(next[:first], next[last+1:]...)
			target = min(first, len(next)-1)
			column = 0
		}
	default:
		return false
	}
	source, visible := richMarkdown(next), richDisplay(next)
	if !r.validModel(next, visible) || !notion.PreservesProtectedObjects(before.text, source) {
		return false
	}
	r.lines, r.source, r.visible = next, source, visible
	start := 0
	for i := 0; i < target; i++ {
		start += len(next[i].display()) + 1
	}
	column = min(column, len(next[target].display()))
	r.anchor, r.head = start+column, start+column
	r.updateModel()
	r.record(before, false)
	r.ensureCursor()
	return true
}

func richLineProtected(line richLine) bool {
	if strings.Contains(line.raw, "\n") {
		return true
	}
	for _, char := range line.chars {
		if char.atom != nil {
			return true
		}
	}
	return false
}
func (r *richEditor) enter() bool {
	if r.anchor != r.head {
		return false
	}
	line, _ := linePoint(r.visible, r.head)
	l := r.lines[line]
	if l.visual == "" {
		return false
	}
	if strings.TrimSpace(charText(l.chars)) == "" {
		r.Block("")
		return true
	}
	if l.prefix != "" && l.heading == 0 && r.head == lineEnd(r.visible, r.head) {
		before := r.snapshot()
		prefix := l.prefix
		if strings.Contains(strings.ToLower(prefix), "[x]") {
			prefix = strings.Replace(strings.Replace(prefix, "[x]", "[ ]", 1), "[X]", "[ ]", 1)
		}
		if m := numberedBlock.FindStringSubmatch(prefix); m != nil {
			n, _ := strconv.Atoi(m[2])
			prefix = m[1] + strconv.Itoa(n+1) + m[3]
		}
		next := parseRich(prefix, r.label)[0]
		r.lines = append(r.lines, richLine{})
		copy(r.lines[line+2:], r.lines[line+1:])
		r.lines[line+1] = next
		r.source, r.visible = richMarkdown(r.lines), richDisplay(r.lines)
		r.anchor, r.head = r.head+1+len(next.display()), r.head+1+len(next.display())
		r.updateModel()
		r.record(before, false)
		r.ensureCursor()
		return true
	}
	r.Replace(r.head, r.head, "\n")
	return true
}
func (r *richEditor) codeFence() bool {
	if r.anchor != r.head {
		return false
	}
	line, _ := linePoint(r.visible, r.head)
	if line >= len(r.lines) || strings.TrimSpace(r.lines[line].raw) != "```" {
		return false
	}
	before := r.snapshot()
	rawSource := strings.Split(before.text, "\n")
	rawSource[line] = literalShortcut("```")
	before.text = strings.Join(rawSource, "\n")
	before.anchor, before.head = lineStart(r.visible, r.head)+3, lineStart(r.visible, r.head)+3
	raw := append([]richLine{}, r.lines[:line]...)
	raw = append(raw, parseRich("```\n\n```", r.label)...)
	r.lines = append(raw, r.lines[line+1:]...)
	r.source, r.visible = richMarkdown(r.lines), richDisplay(r.lines)
	pos := 0
	for i := 0; i <= line; i++ {
		pos += len(r.lines[i].display()) + 1
	}
	r.anchor, r.head = pos, pos
	r.updateModel()
	r.record(before, false)
	r.ensureCursor()
	return true
}
func (r *richEditor) shortcut() {
	line, _ := linePoint(r.visible, r.head)
	l := r.lines[line]
	if l.literal {
		return
	}
	for _, c := range l.chars {
		if c.atom != nil || c.marks != 0 || len(c.wrappers) != 0 || c.code {
			return
		}
	}
	plain := charText(l.chars)
	shortcut := l.prefix + plain
	switch shortcut {
	case "[] ":
		shortcut = "- [ ] "
	case "[x] ", "[X] ":
		shortcut = "- [x] "
	}
	parsed := parseRich(shortcut, r.label)[0]
	if parsed.display() == l.display() {
		return
	}
	// Keep inline shortcuts predictable: convert only when typing at line end.
	start := 0
	for i := 0; i < line; i++ {
		start += len(r.lines[i].display()) + 1
	}
	if r.head != start+len(l.display()) {
		return
	}
	before := r.snapshot()
	raw := strings.Split(before.text, "\n")
	literal := literalShortcut(l.prefix + plain)
	raw[line] = literal
	before.text = strings.Join(raw, "\n")
	r.lines[line] = parsed
	r.head = start + len(parsed.display())
	r.anchor = r.head
	r.updateModel()
	r.record(before, false)
}
func (r *richEditor) charAt(offset int) *richChar {
	line, col := linePoint(r.visible, offset)
	if line >= len(r.lines) {
		return nil
	}
	l := &r.lines[line]
	col -= utf8.RuneCountInString(l.visual)
	if col == len(l.chars) {
		col--
	}
	if col < 0 || col >= len(l.chars) {
		return nil
	}
	return &l.chars[col]
}
func (r *richEditor) openAt(offset int) bool {
	c := r.charAt(offset)
	if c == nil || r.activate == nil {
		return false
	}
	url := c.link
	if c.atom != nil {
		url = c.atom.link
	}
	if url == "" {
		for _, w := range c.wrappers {
			if start := strings.Index(w.open, `discussion-urls="`); start >= 0 {
				url = strings.SplitN(w.open[start+len(`discussion-urls="`):], `"`, 2)[0]
				break
			}
		}
	}
	if url == "" {
		return false
	}
	r.activate(url)
	return true
}

func wordBounds(text string, offset int) (int, int) {
	if text == "" {
		return 0, 0
	}
	offset = min(max(0, offset), len(text)-1)
	// UAX #29 handles accents, combining marks, apostrophes and CJK words.
	start, state := 0, -1
	for rest := text; rest != ""; {
		word, next, nextState := uniseg.FirstWordInString(rest, state)
		end := start + len(word)
		if offset < end {
			return start, end
		}
		start = end
		rest, state = next, nextState
	}
	return len(text), len(text)
}
func (r *richEditor) MouseHandler() func(tview.MouseAction, *tcell.EventMouse, func(tview.Primitive)) (bool, tview.Primitive) {
	return r.WrapMouseHandler(func(action tview.MouseAction, e *tcell.EventMouse, focus func(tview.Primitive)) (bool, tview.Primitive) {
		if r.GetDisabled() {
			return false, nil
		}
		x, y := e.Position()
		r.mouseX, r.mouseY = x, y
		inside := r.InRect(x, y)
		if !inside && !r.drag && !r.scrollDrag {
			return false, nil
		}
		rx, ry, w, h := r.GetInnerRect()
		r.layout()
		switch action {
		case tview.MouseLeftDown:
			focus(r)
			r.typing = nil
			r.lastEdit = time.Time{}
			if x == rx+w+1 && len(r.rows) > h {
				r.scrollDrag = true
				r.scrollTo(y)
				return true, r
			}
			pos := r.point(x, y)
			now := e.When()
			if now.Sub(r.lastClick) < 500*time.Millisecond && abs(x-r.downX) <= 1 && abs(y-r.downY) <= 1 {
				r.clicks = r.clicks%3 + 1
			} else {
				r.clicks = 1
			}
			r.lastClick = now
			r.downX, r.downY = x, y
			r.drag = true
			if e.Modifiers()&tcell.ModShift != 0 {
				r.clicks = 1
				r.Select(r.anchor, pos)
			} else if r.clicks == 2 {
				r.wordStart, r.wordEnd = wordBounds(r.visible, pos)
				r.Select(r.wordStart, r.wordEnd)
			} else if r.clicks == 3 {
				start := strings.LastIndex(r.visible[:pos], "\n") + 1
				end := strings.Index(r.visible[pos:], "\n")
				if end < 0 {
					end = len(r.visible)
				} else {
					end += pos + 1
				}
				r.wordStart, r.wordEnd = start, end
				r.Select(start, end)
			} else {
				r.Select(pos, pos)
			}
			return true, r
		case tview.MouseMove:
			if r.scrollDrag {
				r.scrollTo(y)
				return true, r
			}
			if r.drag {
				r.dragTo()
				return true, r
			}
		case tview.MouseLeftUp:
			if r.scrollDrag {
				r.scrollTo(y)
				r.scrollDrag = false
				return true, nil
			}
			if !r.drag {
				return false, nil
			}
			r.dragTo()
			r.drag = false
			if r.clicks == 1 && r.anchor == r.head && abs(x-r.downX) <= 1 && abs(y-r.downY) <= 1 {
				line, col := linePoint(r.visible, r.head)
				l := r.lines[line]
				if col < utf8.RuneCountInString(l.visual) && strings.Contains(l.visual, "☐") {
					r.Block(strings.Replace(l.prefix, "[ ]", "[x]", 1))
				} else if col < utf8.RuneCountInString(l.visual) && strings.Contains(l.visual, "☑") {
					r.Block(strings.Replace(strings.Replace(l.prefix, "[x]", "[ ]", 1), "[X]", "[ ]", 1))
				} else if c := r.charAt(r.head); c != nil && r.head < lineEnd(r.visible, r.head) && (c.atom != nil || c.link != "" || e.Modifiers()&tcell.ModCtrl != 0) && e.Modifiers()&tcell.ModShift == 0 {
					r.openAt(r.head)
				}
			}
			return true, nil
		case tview.MouseLeftClick, tview.MouseLeftDoubleClick:
			return true, nil // Count presses ourselves; terminal jitter must not defeat double-click.
		case tview.MouseRightClick:
			focus(r)
			pos := r.point(x, y)
			if pos < min(r.anchor, r.head) || pos > max(r.anchor, r.head) || r.anchor == r.head {
				r.Select(pos, pos)
			}
			if r.menu != nil {
				r.menu()
			}
			return true, nil
		case tview.MouseScrollUp, tview.MouseScrollDown:
			offset, _ := r.GetOffset()
			delta := 3
			if action == tview.MouseScrollUp {
				delta = -3
			}
			r.SetOffset(min(max(0, offset+delta), max(0, len(r.rows)-h)), 0)
			if r.drag {
				r.dragTo()
				return true, r
			}
			return true, nil
		}
		_ = ry
		if r.drag || r.scrollDrag {
			return true, r
		}
		return inside, nil
	})
}
func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
func (r *richEditor) dragTo() {
	pos := r.point(r.mouseX, r.mouseY)
	if r.clicks >= 2 {
		start, end := wordBounds(r.visible, pos)
		if r.clicks == 3 {
			start = strings.LastIndex(r.visible[:pos], "\n") + 1
			end = strings.Index(r.visible[pos:], "\n")
			if end < 0 {
				end = len(r.visible)
			} else {
				end += pos + 1
			}
		}
		if pos < r.wordStart {
			r.Select(r.wordEnd, start)
		} else {
			r.Select(r.wordStart, max(r.wordEnd, end))
		}
	} else {
		r.Select(r.anchor, pos)
	}
}
func (r *richEditor) tickMouse() bool {
	if !r.drag {
		return false
	}
	_, y, _, h := r.GetInnerRect()
	delta := 0
	if r.mouseY < y {
		delta = -1 - (y-r.mouseY)/3
	} else if r.mouseY >= y+h {
		delta = 1 + (r.mouseY-y-h)/3
	}
	if delta == 0 {
		return false
	}
	r.layout()
	offset, _ := r.GetOffset()
	r.SetOffset(min(max(0, offset+delta), max(0, len(r.rows)-h)), 0)
	r.dragTo()
	return true
}
func (r *richEditor) scrollTo(y int) {
	_, top, _, h := r.GetInnerRect()
	r.SetOffset(min(max(0, y-top), h-1)*max(0, len(r.rows)-h)/max(1, h-1), 0)
}

// Used by Find so it follows visible text, including formatted words.
func (r *richEditor) Find(query string, next bool) bool {
	if strings.TrimSpace(query) == "" {
		return false
	}
	text := strings.ToLower(r.visible)
	query = strings.ToLower(query)
	from := min(r.head, len(text))
	if !next {
		from = 0
	}
	i := strings.Index(text[from:], query)
	if i >= 0 {
		i += from
	} else {
		i = strings.Index(text[:from], query)
	}
	if i < 0 {
		return false
	}
	r.Select(i, i+len(query))
	r.ensureCursor()
	return true
}
