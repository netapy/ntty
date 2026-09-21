package main

import (
	"html"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
)

// Keep untouched source lines byte-for-byte. Only edited lines are serialized.
// This matters for Notion's extra tags, page references and discussion metadata.
type richChar struct {
	r        rune
	marks    tcell.AttrMask
	wrappers []richWrapper
	atom     *richAtom
	link     string
	code     bool
}
type richWrapper struct{ open, close string }
type richAtom struct{ raw, label, link string }
type richLine struct {
	raw, prefix, visual string
	chars               []richChar
	heading             int
	literal             bool
}

func chars(text string, marks tcell.AttrMask, wrappers []richWrapper) []richChar {
	out := make([]richChar, 0, utf8.RuneCountInString(text))
	for _, r := range text {
		out = append(out, richChar{r: r, marks: marks, wrappers: wrappers})
	}
	return out
}
func atomChars(raw, label, link string) []richChar {
	a := &richAtom{raw, label, link}
	out := chars(label, tcell.AttrDim, nil)
	for i := range out {
		out[i].atom = a
		if link != "" {
			out[i].marks = tcell.AttrUnderline
		}
	}
	return out
}
func charText(cs []richChar) string {
	var b strings.Builder
	for _, c := range cs {
		b.WriteRune(c.r)
	}
	return b.String()
}
func (l richLine) display() string { return l.visual + charText(l.chars) }
func richDisplay(lines []richLine) string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = l.display()
	}
	return strings.Join(out, "\n")
}
func richMarkdown(lines []richLine) string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = l.raw
	}
	return strings.Join(out, "\n")
}

var (
	blockPrefix  = regexp.MustCompile(`^(\s*)(#{1,6} |[-+*] \[[ xX]\] |[-+*] \[[ xX]\]$|[-+*] |\d+[.)] |> )`)
	mentionTag   = regexp.MustCompile(`^<mention-page\b[^>]*\burl="([^"]+)"[^>]*/>`)
	inlineTag    = regexp.MustCompile(`^</?[a-zA-Z][^>]*>`)
	markdownLink = regexp.MustCompile(`^\[((?:\\.|[^\[\]])+)\]\(([^\s]+?)(?:\s+"[^"]*")?\)`)
	meetingLink  = regexp.MustCompile(`\breadOnlyViewMeetingNoteUrl="([^"]+)"`)
)

func parseRich(source string, label func(string) string) []richLine {
	rawLines := strings.Split(source, "\n")
	lines := make([]richLine, 0, len(rawLines))
	fence := ""
	for lineIndex := 0; lineIndex < len(rawLines); lineIndex++ {
		raw := rawLines[lineIndex]
		l := richLine{raw: raw}
		trim := strings.TrimSpace(raw)
		if fence == "" {
			if table, end, ok := parseRichTable(rawLines, lineIndex, label); ok {
				lines = append(lines, table...)
				lineIndex = end
				continue
			}
		}
		if fence == "" && trim == "<tabs>" {
			end := lineIndex + 1
			for end < len(rawLines) && strings.TrimSpace(rawLines[end]) != "</tabs>" {
				end++
			}
			if end < len(rawLines) {
				l.raw = strings.Join(rawLines[lineIndex:end+1], "\n")
				var names []string
				for i := lineIndex + 1; i < end; i++ {
					if strings.TrimSpace(rawLines[i]) != "<tab>" {
						continue
					}
					for i++; i < end; i++ {
						name := strings.TrimSpace(rawLines[i])
						if name == "" || strings.HasPrefix(name, "<") {
							continue
						}
						names = append(names, name)
						break
					}
				}
				name := "Tabs"
				if len(names) > 0 {
					name += " · " + strings.Join(names, " · ")
				}
				l.chars = atomChars(l.raw, "▣ "+name, "notion:current")
				lines = append(lines, l)
				lineIndex = end
				continue
			}
		}
		if fence == "" && strings.HasPrefix(trim, "<meeting-notes ") {
			end := lineIndex + 1
			for end < len(rawLines) && strings.TrimSpace(rawLines[end]) != "</meeting-notes>" {
				end++
			}
			if end < len(rawLines) {
				l.raw = strings.Join(rawLines[lineIndex:end+1], "\n")
				link := ""
				if match := meetingLink.FindStringSubmatch(trim); len(match) == 2 {
					link = html.UnescapeString(match[1])
				}
				title := "Meeting notes"
				for _, candidate := range rawLines[lineIndex+1 : end] {
					candidate = strings.TrimSpace(candidate)
					if candidate == "" || strings.HasPrefix(candidate, "<") {
						continue
					}
					if match := blockPrefix.FindStringSubmatch(candidate); match != nil {
						candidate = candidate[len(match[0]):]
					}
					if visible := strings.TrimSpace(charText(parseInline(candidate, 0, nil, label))); visible != "" {
						title = visible
						break
					}
				}
				if link != "" {
					link = "external:" + link
				}
				l.chars = atomChars(l.raw, "▣ "+title, link)
				lines = append(lines, l)
				lineIndex = end
				continue
			}
		}
		if strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~") {
			marker := trim[:3]
			if fence == "" {
				fence = marker
				name := strings.TrimSpace(trim[3:])
				if name == "" {
					name = "code"
				}
				l.chars = atomChars(raw, "‹ "+name+" ›", "")
			} else if strings.HasPrefix(trim, fence) {
				fence = ""
				l.chars = atomChars(raw, "‹ end code ›", "")
			} else {
				l.chars = chars(raw, 0, nil)
			}
			l.literal = true
		} else if fence != "" {
			l.literal = true
			l.chars = chars(raw, 0, nil)
		} else if trim == "---" || trim == "***" || trim == "___" {
			l.chars = atomChars(raw, "────────────────", "")
		} else if trim == "<empty-block/>" || trim == "<empty-block />" { // A genuinely empty Notion paragraph.
		} else {
			body := raw
			if m := blockPrefix.FindStringSubmatch(raw); m != nil {
				l.prefix = m[0]
				body = raw[len(m[0]):]
				switch token := m[2]; {
				case strings.HasPrefix(token, "#"):
					l.heading = len(strings.TrimSpace(token))
				case strings.Contains(token, "[ ]"):
					l.visual = m[1] + "☐ "
				case strings.Contains(strings.ToLower(token), "[x]"):
					l.visual = m[1] + "☑ "
				case token == "> ":
					l.visual = m[1] + "│ "
				case token == "- " || token == "* " || token == "+ ":
					l.visual = m[1] + "• "
				default:
					l.visual = l.prefix
				}
			}
			l.chars = parseInline(body, 0, nil, label)
		}
		lines = append(lines, l)
	}
	return lines
}

func parseInline(text string, marks tcell.AttrMask, wrappers []richWrapper, label func(string) string) []richChar {
	var out []richChar
	for len(text) > 0 {
		if strings.HasPrefix(text, "$``$") {
			out = append(out, chars("$$", marks, wrappers)...)
			text = text[4:]
			continue
		}
		if text[0] == '\\' && len(text) > 1 && strings.ContainsRune("!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~", rune(text[1])) {
			r, n := utf8.DecodeRuneInString(text[1:])
			out = append(out, chars(string(r), marks, wrappers)...)
			text = text[n+1:]
			continue
		}
		if m := mentionTag.FindStringSubmatch(text); m != nil {
			name := ""
			if label != nil {
				name = label(m[1])
			}
			if name == "" {
				name = "Linked page"
			}
			out = append(out, atomChars(m[0], "↗ "+name, m[1])...)
			text = text[len(m[0]):]
			continue
		}
		if raw, name, link := notionInline(text); raw != "" {
			if id := notionUserID(raw); id != "" && label != nil {
				if resolved := label("user://" + id); resolved != "" {
					name = "@" + resolved
				}
			}
			out = append(out, atomChars(raw, name, link)...)
			text = text[len(raw):]
			continue
		}
		if m := markdownLink.FindStringSubmatch(text); m != nil {
			if m[1] == m[2] {
				if name := linkLabel(html.UnescapeString(m[2])); name != "" {
					out = append(out, atomChars(m[0], name, html.UnescapeString(m[2]))...)
					text = text[len(m[0]):]
					continue
				}
			}
			ws := append(append([]richWrapper{}, wrappers...), richWrapper{"[", m[0][len(m[1])+1:]})
			cs := parseInline(m[1], marks, ws, label)
			for i := range cs {
				cs[i].link = html.UnescapeString(m[2])
			}
			out = append(out, cs...)
			text = text[len(m[0]):]
			continue
		}
		if strings.HasPrefix(text, "<span ") || strings.HasPrefix(text, "<span>") {
			if end := strings.IndexByte(text, '>'); end >= 0 {
				if close := closingSpan(text[end+1:]); close >= 0 {
					open := text[:end+1]
					inner := text[end+1 : end+1+close]
					ws := append(append([]richWrapper{}, wrappers...), richWrapper{open, "</span>"})
					spanMarks := marks
					if strings.Contains(open, `underline="true"`) {
						spanMarks |= tcell.AttrUnderline
					}
					cs := parseInline(inner, spanMarks, ws, label)
					if inner == "" {
						cs = atomChars(open+"</span>", "◇", "")
					}
					out = append(out, cs...)
					text = text[end+1+close+len("</span>"):]
					continue
				}
			}
		}
		if m := inlineTag.FindString(text); m != "" {
			name := m
			switch {
			case strings.HasPrefix(m, "<callout"):
				name = "▎ Note"
			case m == "</callout>":
				name = "▎"
			case m == "<br>" || m == "<br/>":
				name = " ↵ "
			case strings.HasPrefix(m, "<unknown"):
				name = "[Notion block]"
			}
			out = append(out, atomChars(m, name, "")...)
			text = text[len(m):]
			continue
		}
		matched := false
		for _, d := range []struct {
			s    string
			mark tcell.AttrMask
		}{{"***", tcell.AttrBold | tcell.AttrItalic}, {"**", tcell.AttrBold}, {"__", tcell.AttrBold}, {"~~", tcell.AttrStrikeThrough}, {"*", tcell.AttrItalic}, {"_", tcell.AttrItalic}} {
			if strings.HasPrefix(d.s, "_") && len(out) > 0 && unicode.IsLetter(out[len(out)-1].r) {
				continue
			}
			if strings.HasPrefix(text, d.s) {
				if end := strings.Index(text[len(d.s):], d.s); end > 0 {
					body := text[len(d.s) : len(d.s)+end]
					if strings.TrimSpace(body) != "" {
						out = append(out, parseInline(body, marks|d.mark, wrappers, label)...)
						text = text[end+2*len(d.s):]
						matched = true
						break
					}
				}
			}
		}
		if matched {
			continue
		}
		if text[0] == '`' {
			n := 0
			for n < len(text) && text[n] == '`' {
				n++
			}
			delim := text[:n]
			if end := strings.Index(text[n:], delim); end >= 0 {
				raw := text[:n+end+n]
				body := text[n : n+end]
				if strings.HasPrefix(body, " ") && strings.HasSuffix(body, " ") && strings.TrimSpace(body) != "" {
					body = body[1 : len(body)-1]
				}
				cs := chars(body, marks, wrappers)
				for i := range cs {
					cs[i].code = true
				}
				out = append(out, cs...)
				text = text[len(raw):]
				continue
			}
		}
		if text[0] == '&' {
			if end := strings.IndexByte(text, ';'); end > 0 && end < 16 {
				entity := text[:end+1]
				decoded := html.UnescapeString(entity)
				if decoded != entity {
					out = append(out, chars(decoded, marks, wrappers)...)
					text = text[end+1:]
					continue
				}
			}
		}
		r, n := utf8.DecodeRuneInString(text)
		out = append(out, chars(string(r), marks, wrappers)...)
		text = text[n:]
	}
	return out
}

func closingSpan(text string) int {
	depth := 1
	for i := 0; i < len(text); i++ {
		if strings.HasPrefix(text[i:], "<span ") || strings.HasPrefix(text[i:], "<span>") {
			depth++
		}
		if strings.HasPrefix(text[i:], "</span>") {
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func wrappersFor(c richChar) []richWrapper {
	w := append([]richWrapper{}, c.wrappers...)
	if c.marks&tcell.AttrBold != 0 {
		w = append(w, richWrapper{"**", "**"})
	}
	if c.marks&tcell.AttrItalic != 0 {
		w = append(w, richWrapper{"*", "*"})
	}
	if c.marks&tcell.AttrStrikeThrough != 0 {
		w = append(w, richWrapper{"~~", "~~"})
	}
	if c.marks&tcell.AttrUnderline != 0 {
		found := false
		for _, v := range w {
			if strings.Contains(v.open, `underline="true"`) {
				found = true
			}
		}
		if !found {
			w = append(w, richWrapper{`<span underline="true">`, "</span>"})
		}
	}
	return w
}

func serializeLine(l *richLine) {
	var b strings.Builder
	b.WriteString(l.prefix)
	var active []richWrapper
	var previousAtom *richAtom
	for index := 0; index < len(l.chars); index++ {
		c := l.chars[index]
		ws := wrappersFor(c)
		if c.atom != nil {
			ws = c.wrappers
		}
		common := 0
		for common < len(active) && common < len(ws) && active[common] == ws[common] {
			common++
		}
		for i := len(active) - 1; i >= common; i-- {
			b.WriteString(active[i].close)
		}
		for _, w := range ws[common:] {
			b.WriteString(w.open)
		}
		active = ws
		if c.code {
			end := index + 1
			for end < len(l.chars) && l.chars[end].code {
				end++
			}
			code := charText(l.chars[index:end])
			longest, run := 0, 0
			for _, v := range code {
				if v == '`' {
					run++
					longest = max(longest, run)
				} else {
					run = 0
				}
			}
			delim := strings.Repeat("`", longest+1)
			pad := ""
			if strings.HasPrefix(code, "`") || strings.HasSuffix(code, "`") || strings.HasPrefix(code, " ") && strings.HasSuffix(code, " ") {
				pad = " "
			}
			b.WriteString(delim + pad + code + pad + delim)
			index = end - 1
			previousAtom = nil
			continue
		}
		if c.atom != nil {
			if c.atom != previousAtom {
				b.WriteString(c.atom.raw)
			}
			previousAtom = c.atom
			continue
		}
		previousAtom = nil
		if !l.literal && strings.ContainsRune("\\*_~`[]<>#$", c.r) {
			b.WriteByte('\\')
		}
		if !l.literal && c.r == '&' {
			b.WriteString("&amp;")
		} else {
			b.WriteRune(c.r)
		}
	}
	for i := len(active) - 1; i >= 0; i-- {
		b.WriteString(active[i].close)
	}
	l.raw = b.String()
	if !l.literal && l.prefix == "" && len(l.chars) == 0 {
		l.raw = "<empty-block/>"
	}
}

func linePoint(text string, offset int) (line, column int) {
	offset = min(max(offset, 0), len(text))
	for _, r := range text[:offset] {
		if r == '\n' {
			line++
			column = 0
		} else {
			column++
		}
	}
	return
}

func displayChars(l richLine) []richChar { return append(chars(l.visual, 0, nil), l.chars...) }

// Apply the actual visible edit, retaining formatting on surviving characters.
func editRich(lines []richLine, old, new string) ([]richLine, bool) {
	a, b := []rune(old), []rune(new)
	start := 0
	for start < len(a) && start < len(b) && a[start] == b[start] {
		start++
	}
	endA, endB := len(a), len(b)
	for endA > start && endB > start && a[endA-1] == b[endB-1] {
		endA--
		endB--
	}
	return editRichAt(lines, old, len(string(a[:start])), len(string(a[:endA])), string(b[start:endB]))
}

func editRichAt(lines []richLine, old string, start, end int, inserted string) ([]richLine, bool) {
	from, col := linePoint(old, start)
	to, endCol := linePoint(old, end)
	left, right := displayChars(lines[from]), displayChars(lines[to])
	// Page mentions/unknown tags are objects, not editable tag source. A whole object may be removed.
	if col > 0 && col < len(left) && left[col].atom != nil && left[col-1].atom == left[col].atom {
		return lines, false
	}
	if endCol > 0 && endCol < len(right) && right[endCol].atom != nil && right[endCol-1].atom == right[endCol].atom {
		return lines, false
	}
	style := richChar{}
	if col < len(left) {
		style = left[col]
	} else if col > 0 {
		style = left[col-1]
	}
	if style.atom != nil {
		empty := style.atom.raw
		style = richChar{}
		if close := strings.Index(empty, ">"); close >= 0 && strings.HasPrefix(empty, "<td") && empty[close+1:] == "</td>" && from == to && col == endCol && inserted != "" {
			style.wrappers = []richWrapper{{empty[:close+1], "</td>"}}
			endCol++ // Replace the visible empty-cell placeholder.
		}
		// At a cell's right edge, insertion belongs to that cell rather than
		// the invisible XML separator following it.
		if col > 0 && left[col-1].atom == nil {
			for _, wrapper := range left[col-1].wrappers {
				if strings.HasPrefix(wrapper.open, "<td") {
					style = left[col-1]
					break
				}
			}
		}
	}
	parts := strings.Split(inserted, "\n")
	emptyCell := ""
	if from == to && inserted == "" && col < endCol && (col == 0 || left[col-1].atom != nil || len(left[col-1].wrappers) == 0) && (endCol == len(right) || right[endCol].atom != nil) {
		plain := true
		for _, c := range left[col:endCol] {
			if c.atom != nil {
				plain = false
			}
		}
		if plain {
			for _, w := range left[col].wrappers {
				if strings.HasPrefix(w.open, "<td") {
					emptyCell = w.open + w.close
					break
				}
			}
		}
	}
	var replacement []richLine
	for i, p := range parts {
		l := richLine{literal: lines[from].literal}
		if i == 0 {
			l.prefix = lines[from].prefix
			l.visual = lines[from].visual
			l.heading = lines[from].heading
			l.chars = append(l.chars, left[:col]...)
		}
		insert := chars(p, style.marks, style.wrappers)
		if emptyCell != "" {
			insert = styledAtomChars(emptyCell, " ", 0)
		}
		for j := range insert {
			insert[j].link = style.link
			insert[j].code = style.code
		}
		l.chars = append(l.chars, insert...)
		if i == len(parts)-1 {
			l.chars = append(l.chars, right[endCol:]...)
		}
		text := charText(l.chars)
		if l.visual != "" {
			if strings.HasPrefix(text, l.visual) {
				l.chars = l.chars[len([]rune(l.visual)):]
			} else {
				l.prefix = ""
				l.visual = ""
				l.heading = 0
			}
		}
		if l.visual == "" && !l.literal {
			for _, v := range []struct{ visual, prefix string }{{"☐ ", "- [ ] "}, {"☑ ", "- [x] "}, {"• ", "- "}, {"│ ", "> "}} {
				if strings.HasPrefix(charText(l.chars), v.visual) {
					l.visual = v.visual
					l.prefix = v.prefix
					l.chars = l.chars[len([]rune(v.visual)):]
					break
				}
			}
		}
		serializeLine(&l)
		replacement = append(replacement, l)
	}
	out := append([]richLine{}, lines[:from]...)
	out = append(out, replacement...)
	out = append(out, lines[to+1:]...)
	return out, true
}

func unescapeURL(s string) string { return html.UnescapeString(s) }
