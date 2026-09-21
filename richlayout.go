package main

import (
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/rivo/uniseg"
)

type richCell struct {
	start, end, x, width int
	marks                tcell.AttrMask
}
type richRow struct {
	start, end int
	cells      []richCell
}

// Use the same Unicode line-break rules as TextArea, once per edit/resize.
// Painting and pointer hit-testing then visit only the visible rows.
func layoutRich(text string, lines []richLine, width int) []richRow {
	if width < 1 {
		return nil
	}
	var cells []richCell
	var breaks []int
	line, column, offset, state := 0, 0, 0, -1
	lineWidth, sinceBreak, lastBreak := 0, 0, -1
	for rest := text; rest != ""; {
		cluster, next, boundaries, nextState := uniseg.StepString(rest, state)
		w := boundaries >> uniseg.ShiftWidth
		if cluster == "\t" {
			w = tview.TabSize
		}
		marks := tcell.AttrMask(0)
		if line < len(lines) {
			l := lines[line]
			if l.heading > 0 {
				marks |= tcell.AttrBold
			}
			index := column - utf8.RuneCountInString(l.visual)
			if index >= 0 && index < len(l.chars) {
				c := l.chars[index]
				marks |= c.marks
				if c.code {
					marks |= tcell.AttrDim
				}
				if c.link != "" {
					marks |= tcell.AttrUnderline
				}
				for _, wrap := range c.wrappers {
					if strings.Contains(wrap.open, `underline="true"`) || strings.Contains(wrap.open, "discussion-urls=") {
						marks |= tcell.AttrUnderline
					}
				}
			} else if l.visual != "" {
				marks |= tcell.AttrDim
			}
		}
		cells = append(cells, richCell{start: offset, end: offset + len(cluster), width: w, marks: marks})
		lineWidth += w
		sinceBreak += w
		if lineWidth <= width {
			if boundaries&uniseg.MaskLine == uniseg.LineMustBreak && (next != "" || uniseg.HasTrailingLineBreakInString(cluster)) {
				breaks = append(breaks, len(cells))
				lineWidth, sinceBreak, lastBreak = 0, 0, -1
			}
		} else if lastBreak >= 0 {
			breaks = append(breaks, lastBreak)
			lineWidth = sinceBreak
			lastBreak = -1
		} else if len(cells) > 1 && (len(breaks) == 0 || breaks[len(breaks)-1] < len(cells)-1) {
			breaks = append(breaks, len(cells)-1)
			lineWidth = w
		}
		if boundaries&uniseg.MaskLine == uniseg.LineCanBreak {
			lastBreak = len(cells)
			sinceBreak = 0
		}
		if strings.Contains(cluster, "\n") {
			line++
			column = 0
		} else {
			column += utf8.RuneCountInString(cluster)
		}
		offset += len(cluster)
		rest, state = next, nextState
	}
	if len(breaks) == 0 || breaks[len(breaks)-1] != len(cells) {
		breaks = append(breaks, len(cells))
	}
	rows := make([]richRow, 0, len(breaks)+1)
	start := 0
	for _, end := range breaks {
		row := richRow{start: 0, end: len(text), cells: cells[start:end]}
		if start < len(cells) {
			row.start = cells[start].start
		} else {
			row.start = len(text)
		}
		if end < len(cells) {
			row.end = cells[end].start
		}
		x := 0
		for i := range row.cells {
			row.cells[i].x = x
			x += row.cells[i].width
		}
		rows = append(rows, row)
		start = end
	}
	if strings.HasSuffix(text, "\n") {
		rows = append(rows, richRow{start: len(text), end: len(text)})
	}
	return rows
}

func (r *richEditor) layout() {
	_, _, width, _ := r.GetInnerRect()
	if r.layoutWidth == width && r.rows != nil {
		return
	}
	if r.layoutWidth != width {
		r.visualRow = -1
	}
	r.rows = layoutRich(r.visible, r.lines, width)
	r.layoutWidth = width
}

func (r *richEditor) position(offset int) (row, column int) {
	r.layout()
	if len(r.rows) == 0 {
		return
	}
	row = sort.Search(len(r.rows), func(i int) bool { return r.rows[i].start > offset }) - 1
	row = max(0, row)
	for _, c := range r.rows[row].cells {
		if c.start >= offset {
			return row, c.x
		}
		column = c.x + c.width
	}
	if column >= r.layoutWidth {
		return row + 1, 0
	}
	return
}

func (r *richEditor) cursorPosition() (row, column int) {
	r.layout()
	if r.visualRow < 0 || r.visualRow >= len(r.rows) {
		return r.position(r.head)
	}
	line := r.rows[r.visualRow]
	if r.head < line.start || r.head > line.end {
		return r.position(r.head)
	}
	for _, cell := range line.cells {
		if cell.start >= r.head {
			return r.visualRow, cell.x
		}
		column = cell.x + cell.width
	}
	return r.visualRow, column
}

func (r *richEditor) point(x, y int) int {
	r.layout()
	rx, ry, width, height := r.GetInnerRect()
	if len(r.rows) == 0 || width < 1 || height < 1 {
		return 0
	}
	rowOffset, _ := r.GetOffset()
	row := min(max(0, y-ry), height-1) + rowOffset
	if row >= len(r.rows) {
		return len(r.visible)
	}
	column := min(max(0, x-rx), width)
	line := r.rows[row]
	for _, c := range line.cells {
		if c.width > 0 && column < c.x+c.width {
			return c.start
		}
	}
	end := line.end
	if end > line.start && r.visible[end-1] == '\n' {
		end--
	}
	return end
}

type richScreen struct {
	tcell.Screen
	editor *richEditor
}

func (s richScreen) SetContent(x, y int, main rune, combining []rune, style tcell.Style) {
	r := s.editor
	rx, ry, w, h := r.GetInnerRect()
	if x >= rx && x < rx+w && y >= ry && y < ry+h {
		offset, _ := r.GetOffset()
		row := y - ry + offset
		column := x - rx
		if row < len(r.rows) {
			cs := r.rows[row].cells
			i := sort.Search(len(cs), func(i int) bool { return cs[i].x >= column })
			if i < len(cs) && cs[i].x == column {
				_, _, attr := style.Decompose()
				style = style.Attributes(attr | cs[i].marks)
				if (attr|cs[i].marks)&tcell.AttrUnderline != 0 {
					style = style.Underline(true)
				} // tcell also needs its underline-style field set.
			}
		}
	}
	s.Screen.SetContent(x, y, main, combining, style)
}

func (r *richEditor) Draw(screen tcell.Screen) {
	r.layout()
	r.TextArea.Draw(richScreen{screen, r})
	x, y, w, h := r.GetInnerRect()
	offset, _ := r.GetOffset()
	line, lineStart := 0, 0
	for row := offset; row < len(r.rows) && row < offset+h; row++ {
		for line+1 < len(r.lines) && r.rows[row].start > lineStart+len(r.lines[line].display()) {
			lineStart += len(r.lines[line].display()) + 1
			line++
		}
		if line < len(r.lines) && r.rows[row].start == lineStart && r.lines[line].heading > 0 {
			marker := strings.Repeat("#", r.lines[line].heading)
			for i, ch := range marker {
				screen.SetContent(x-len(marker)-1+i, y+row-offset, ch, nil, accent)
			}
		}
	}
	if len(r.rows) > h && h > 0 {
		thumb := max(1, h*h/len(r.rows))
		top := offset * (h - thumb) / max(1, len(r.rows)-h)
		for n := 0; n < h; n++ {
			glyph := '│'
			if n >= top && n < top+thumb {
				glyph = '┃'
			}
			screen.SetContent(x+w+1, y+n, glyph, nil, quiet)
		}
	}
	if r.HasFocus() && !r.GetDisabled() {
		row, column := r.cursorPosition()
		if row >= offset && row < offset+h && column < w {
			screen.ShowCursor(x+column, y+row-offset)
		} else {
			screen.HideCursor()
		}
	}
}
