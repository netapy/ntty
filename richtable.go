package main

import (
	"regexp"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/mattn/go-runewidth"
)

var tableCellTag = regexp.MustCompile(`(?s)([ \t]*)(<td\b[^>]*>)(.*?)(</td>)`)

type richTableCell struct {
	open, text, close string
	between           string
}

type richTableRow struct {
	raw, prefix, suffix string
	cells               []richTableCell
}

func startsTag(line, name string) bool {
	line = strings.TrimSpace(line)
	prefix := "<" + name
	if !strings.HasPrefix(line, prefix) || len(line) == len(prefix) {
		return false
	}
	next := line[len(prefix)]
	return next == '>' || next == ' ' || next == '\t'
}

// parseRichTable groups the verbose enhanced-Markdown XML into one visible
// line per table row. Cell text stays editable; structural tags and whitespace
// are invisible atoms, so serialization remains byte-for-byte exact.
func parseRichTable(source []string, start int, label func(string) string) ([]richLine, int, bool) {
	if start < 0 || start >= len(source) || !startsTag(source[start], "table") {
		return nil, start, false
	}
	end := start
	for ; end < len(source); end++ {
		if strings.Contains(source[end], "</table>") {
			break
		}
	}
	if end >= len(source) {
		return nil, start, false
	}
	block := source[start : end+1]
	tableRaw := strings.Join(block, "\n")
	type lineRange struct{ start, end int }
	var ranges []lineRange
	for i := 0; i < len(block); i++ {
		if !startsTag(block[i], "tr") {
			continue
		}
		rowEnd := i
		for ; rowEnd < len(block); rowEnd++ {
			if strings.Contains(block[rowEnd], "</tr>") {
				break
			}
		}
		if rowEnd >= len(block) {
			return nil, start, false
		}
		ranges = append(ranges, lineRange{i, rowEnd})
		i = rowEnd
	}
	if len(ranges) == 0 {
		return []richLine{{raw: tableRaw, chars: styledAtomChars(tableRaw, "▦ Table", tcell.AttrDim)}}, end, true
	}

	rows := make([]richTableRow, 0, len(ranges))
	for index, rowRange := range ranges {
		if index > 0 {
			rowRange.start = ranges[index-1].end + 1
		}
		raw := strings.Join(block[rowRange.start:rowRange.end+1], "\n")
		matches := tableCellTag.FindAllStringSubmatchIndex(raw, -1)
		row := richTableRow{raw: raw}
		if len(matches) == 0 {
			row.prefix, row.suffix = raw, ""
			rows = append(rows, row)
			continue
		}
		row.prefix = raw[:matches[0][4]] // Everything before the first <td> tag.
		for i, match := range matches {
			cell := richTableCell{
				open:  raw[match[4]:match[5]],
				text:  raw[match[6]:match[7]],
				close: raw[match[8]:match[9]],
			}
			if i+1 < len(matches) {
				cell.between = raw[match[9]:matches[i+1][4]]
			} else {
				row.suffix = raw[match[9]:]
			}
			row.cells = append(row.cells, cell)
		}
		rows = append(rows, row)
	}
	// Unsupported/malformed structure must stay visible as one source object,
	// never hide text between cells or present unequal rows as an editable grid.
	for _, row := range rows {
		if len(row.cells) != len(rows[0].cells) || len(row.cells) == 0 {
			return nil, start, false
		}
		for _, cell := range row.cells {
			if strings.TrimSpace(cell.between) != "" {
				return nil, start, false
			}
		}
	}

	var widths []int
	for _, row := range rows {
		for column, cell := range row.cells {
			for len(widths) <= column {
				widths = append(widths, 1)
			}
			var display strings.Builder
			for _, c := range parseInline(cell.text, 0, nil, label) {
				display.WriteRune(c.r)
			}
			widths[column] = max(widths[column], runewidth.StringWidth(display.String()))
		}
	}
	border := func(left, middle, right string) string {
		parts := make([]string, len(widths))
		for i, width := range widths {
			parts[i] = strings.Repeat("─", width+2)
		}
		return left + strings.Join(parts, middle) + right
	}
	var out []richLine
	if top := strings.Join(block[:ranges[0].start], "\n"); top != "" {
		out = append(out, richLine{raw: top, chars: styledAtomChars(top, border("┌", "┬", "┐"), tcell.AttrDim)})
	}
	for _, row := range rows {
		if len(row.cells) == 0 {
			out = append(out, richLine{raw: row.raw, chars: styledAtomChars(row.raw, "│ Table row │", tcell.AttrDim)})
			continue
		}
		line := richLine{raw: row.raw, prefix: row.prefix, visual: "│ "}
		for column, cell := range row.cells {
			marks := tcell.AttrMask(0)
			visible := parseInline(cell.text, marks, []richWrapper{{cell.open, cell.close}}, label)
			for i := range visible {
				if visible[i].atom != nil {
					visible[i].wrappers = []richWrapper{{cell.open, cell.close}}
				}
			}
			if len(visible) == 0 {
				visible = styledAtomChars(cell.open+cell.close, " ", marks)
			}
			line.chars = append(line.chars, visible...)
			var display strings.Builder
			for _, c := range visible {
				display.WriteRune(c.r)
			}
			padding := strings.Repeat(" ", max(0, widths[column]-runewidth.StringWidth(display.String())))
			if column+1 < len(row.cells) {
				line.chars = append(line.chars, styledAtomChars(cell.between, padding+" │ ", tcell.AttrDim)...)
			} else {
				line.chars = append(line.chars, styledAtomChars(row.suffix, padding+" │", tcell.AttrDim)...)
			}
		}
		out = append(out, line)
	}
	if bottom := strings.Join(block[ranges[len(ranges)-1].end+1:], "\n"); bottom != "" {
		out = append(out, richLine{raw: bottom, chars: styledAtomChars(bottom, border("└", "┴", "┘"), tcell.AttrDim)})
	}
	if richMarkdown(out) != tableRaw {
		return []richLine{{raw: tableRaw, chars: styledAtomChars(tableRaw, "▦ Table", tcell.AttrDim)}}, end, true
	}
	return out, end, true
}

func styledAtomChars(raw, label string, marks tcell.AttrMask) []richChar {
	result := atomChars(raw, label, "")
	for i := range result {
		result[i].marks = marks
	}
	return result
}

// Tab/Enter navigate cells instead of splitting XML wrappers into extra cells.
func (r *richEditor) navigateTableCell(back bool) bool {
	if r.anchor != r.head {
		return false
	}
	line, _ := linePoint(r.visible, r.head)
	if line >= len(r.lines) || !strings.Contains(r.lines[line].raw, "<td") {
		return false
	}
	first := line
	for first > 0 && !startsTag(r.lines[first].raw, "table") {
		first--
	}
	if !startsTag(r.lines[first].raw, "table") {
		return false
	}
	var positions []int
	offset := 0
	for i, l := range r.lines {
		if i >= first {
			inCell := false
			pos := offset + len(l.visual)
			for _, c := range l.chars {
				cell := false
				for _, w := range c.wrappers {
					if strings.HasPrefix(w.open, "<td") {
						cell = true
					}
				}
				if c.atom != nil && startsTag(c.atom.raw, "td") {
					cell = true
				}
				if cell && !inCell {
					positions = append(positions, pos)
				}
				inCell = cell
				pos += len(string(c.r))
			}
			if strings.HasSuffix(strings.TrimSpace(l.raw), "</table>") {
				break
			}
		}
		offset += len(l.display()) + 1
	}
	if len(positions) == 0 {
		return true
	}
	target := positions[0]
	if back {
		for _, pos := range positions {
			if pos < r.head {
				target = pos
			}
		}
	} else {
		target = positions[len(positions)-1]
		for _, pos := range positions {
			if pos > r.head {
				target = pos
				break
			}
		}
	}
	r.Select(target, target)
	r.ensureCursor()
	return true
}
