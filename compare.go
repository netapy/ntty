package main

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func selectComparison(ui *tview.Application, pane *tview.Flex, local, remote *tview.TextView, keepLocal, keepRemote func()) {
	selected := 0
	views := []*tview.TextView{local, remote}
	titles := []string{"Local draft", "Notion"}
	choose := func(index int) {
		selected = index
		for i, view := range views {
			view.SetBorderStyle(quiet).SetTitle(" " + titles[i] + " ")
			if i == index {
				view.SetBorderStyle(accent).SetTitle(" › " + titles[i] + " · Enter to keep ")
			}
		}
		ui.SetFocus(views[index])
	}
	pane.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		switch e.Key() {
		case tcell.KeyLeft:
			choose(0)
			return nil
		case tcell.KeyRight:
			choose(1)
			return nil
		case tcell.KeyEnter:
			if selected == 0 {
				keepLocal()
			} else {
				keepRemote()
			}
			return nil
		}
		return e
	})
	for i, view := range views {
		view.SetMouseCapture(func(action tview.MouseAction, e *tcell.EventMouse) (tview.MouseAction, *tcell.EventMouse) {
			if action == tview.MouseLeftClick && view.InRect(e.Position()) {
				choose(i)
				return tview.MouseConsumed, nil
			}
			return action, e
		})
	}
	choose(0)
}
