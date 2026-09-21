package main

import (
	"strings"
	"unicode"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// Hover is paint only: moving the mouse never changes keyboard selection,
// focus, the editor's selection, or a list's preview callbacks.
func installHover(ui *tview.Application, root tview.Primitive) {
	x, y, visible, dragging := 0, 0, false, false
	mouse, key, draw := ui.GetMouseCapture(), ui.GetInputCapture(), ui.GetAfterDrawFunc()
	ui.SetMouseCapture(func(e *tcell.EventMouse, action tview.MouseAction) (*tcell.EventMouse, tview.MouseAction) {
		before := hoverRect{}
		if visible {
			before = hoverAt(root, x, y)
		}
		x, y = e.Position()
		if action == tview.MouseScrollUp || action == tview.MouseScrollDown || action == tview.MouseScrollLeft || action == tview.MouseScrollRight {
			if target := scrollableAt(root, x, y); target != nil {
				if consumed, _ := target.MouseHandler()(action, e, func(tview.Primitive) {}); consumed {
					if list, ok := target.(*tview.List); ok && list.GetItemCount() > 0 {
						// List.Draw forces the highlighted item into view. Keep it
						// within the newly scrolled viewport so drawing cannot undo
						// the wheel movement immediately.
						offset, _ := list.GetOffset()
						_, _, _, height := list.GetInnerRect()
						rows := max(1, height)
						if _, secondary := list.GetItemText(0); secondary != "" {
							rows = max(1, height/2)
						}
						list.SetCurrentItem(min(max(list.GetCurrentItem(), offset), offset+rows-1))
					}
					ui.ForceDraw()
					return nil, action
				}
			}
		}
		if action == tview.MouseLeftDown || action == tview.MouseRightDown || action == tview.MouseMiddleDown {
			dragging = true
		}
		visible = !dragging && e.Buttons() == tcell.ButtonNone
		if action == tview.MouseLeftUp || action == tview.MouseRightUp || action == tview.MouseMiddleUp {
			dragging = false
		}
		after := hoverRect{}
		if visible {
			after = hoverAt(root, x, y)
		}
		if before != after {
			ui.ForceDraw()
		}
		if mouse != nil {
			return mouse(e, action)
		}
		return e, action
	})
	ui.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		visible = false
		if key != nil {
			return key(e)
		}
		return e
	})
	ui.SetAfterDrawFunc(func(screen tcell.Screen) {
		if draw != nil {
			draw(screen)
		}
		if visible {
			hoverAt(root, x, y).paint(screen)
		}
	})
}

// Route the wheel to the deepest pane under the pointer. tview's normal Flex
// dispatch stops at the first child that consumes an event, which can send a
// wheel event to a sidebar list whose stale rectangle overlaps the editor after
// a resize. Resolving the concrete leaf first also scrolls without stealing
// keyboard focus from the editor or a palette query.
func scrollableAt(p tview.Primitive, x, y int) tview.Primitive {
	if p == nil {
		return nil
	}
	rx, ry, width, height := p.GetRect()
	if width <= 0 || height <= 0 || x < rx || x >= rx+width || y < ry || y >= ry+height {
		return nil
	}
	switch p := p.(type) {
	case *tview.Pages:
		_, front := p.GetFrontPage()
		return scrollableAt(front, x, y)
	case *tview.Flex:
		for i := p.GetItemCount() - 1; i >= 0; i-- {
			if target := scrollableAt(p.GetItem(i), x, y); target != nil {
				return target
			}
		}
	case *tview.Form:
		for i := p.GetFormItemCount() - 1; i >= 0; i-- {
			if target := scrollableAt(p.GetFormItem(i), x, y); target != nil {
				return target
			}
		}
	case *richEditor, *tview.List, *tview.Table, *tview.TextView, *tview.TextArea:
		return p
	}
	return nil
}

func listAt(p tview.Primitive, x, y int) *tview.List {
	if list, ok := scrollableAt(p, x, y).(*tview.List); ok {
		return list
	}
	return nil
}

type hoverRect struct{ x, y, width, height int }

type breadcrumbView struct {
	*tview.TextView
	app *app
}

func (b *breadcrumbView) Draw(screen tcell.Screen) {
	// This is one navigation line, not a scrolling TextView. Paint it once
	// using the current rectangle, with no retained wrapping/scroll state.
	b.Box.DrawForSubclass(screen, b)
	x, y, width, height := b.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}
	b.app.renderBreadcrumb(width)
	tview.Print(screen, b.GetText(false), x, y, width, tview.AlignLeft, tcell.ColorTeal)
}

func hoverAt(p tview.Primitive, x, y int) hoverRect {
	if p == nil {
		return hoverRect{}
	}
	rx, ry, w, h := p.GetRect()
	if x < rx || x >= rx+w || y < ry || y >= ry+h {
		return hoverRect{}
	}
	switch p := p.(type) {
	case *tview.Pages:
		_, front := p.GetFrontPage()
		return hoverAt(front, x, y) // Modal padding must not hover the page underneath.
	case *tview.Flex:
		for i := p.GetItemCount() - 1; i >= 0; i-- {
			if hit := hoverAt(p.GetItem(i), x, y); hit.width > 0 {
				return hit
			}
		}
	case *tview.Form:
		for i := 0; i < p.GetButtonCount(); i++ {
			if hit := hoverAt(p.GetButton(i), x, y); hit.width > 0 {
				return hit
			}
		}
		for i := 0; i < p.GetFormItemCount(); i++ {
			if hit := hoverAt(p.GetFormItem(i), x, y); hit.width > 0 {
				return hit
			}
		}
	case *tview.List:
		rx, ry, w, h = p.GetInnerRect()
		if x < rx || x >= rx+w || y < ry || y >= ry+h || p.GetItemCount() == 0 {
			break
		}
		rowHeight := 1
		// ntty's two-line lists always supply secondary metadata; its compact
		// sidebar/command lists supply main text only.
		if _, secondary := p.GetItemText(0); secondary != "" {
			rowHeight = 2
		}
		offset, _ := p.GetOffset()
		row := (y - ry) / rowHeight
		if offset+row >= p.GetItemCount() {
			break
		}
		return hoverRect{rx, ry + row*rowHeight, w, min(rowHeight, h-row*rowHeight)}
	case *tview.Button:
		if !p.IsDisabled() {
			rx, ry, w, h = p.GetInnerRect()
			return hoverRect{rx, ry, w, h}
		}
	case *breadcrumbView:
		rx, ry, w, h = p.GetInnerRect()
		if y != ry || h < 1 {
			break
		}
		for _, link := range p.app.breadcrumbLinks {
			end := min(link.end, w)
			if x >= rx+link.start && x < rx+end {
				return hoverRect{rx + link.start, ry, end - link.start, 1}
			}
		}
	case *tview.TextView:
		if p.GetMouseCapture() != nil && strings.TrimSpace(p.GetText(true)) != "" {
			rx, ry, w, h = p.GetInnerRect()
			return hoverRect{rx, ry, w, min(1, h)}
		}
	}
	return hoverRect{}
}

func (r hoverRect) paint(screen tcell.Screen) {
	for y := r.y; y < r.y+r.height; y++ {
		for x := r.x; x < r.x+r.width; {
			main, combining, style, width := screen.GetContent(x, y)
			if !unicode.IsSpace(main) && main != 0 && !sameTerminalStyle(style, selection) && !sameTerminalStyle(style, navigationSelection) {
				screen.SetContent(x, y, main, combining, style.Bold(true).Dim(false))
			}
			x += max(1, width)
		}
	}
}

func sameTerminalStyle(a, b tcell.Style) bool {
	aForeground, aBackground, aAttributes := a.Decompose()
	bForeground, bBackground, bAttributes := b.Decompose()
	return aForeground == bForeground && aBackground == bBackground && aAttributes == bAttributes
}
