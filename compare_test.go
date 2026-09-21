package main

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"testing"
)

func TestCompareKeyboardAndClickChooseVersion(t *testing.T) {
	ui := tview.NewApplication()
	left, right := tview.NewTextView(), tview.NewTextView()
	left.SetRect(0, 0, 30, 12)
	right.SetRect(30, 0, 30, 12)
	pane := tview.NewFlex().AddItem(left, 0, 1, true).AddItem(right, 0, 1, false)
	kept := ""
	selectComparison(ui, pane, left, right, func() { kept = "local" }, func() { kept = "remote" })
	key := pane.GetInputCapture()
	key(tcell.NewEventKey(tcell.KeyRight, 0, 0))
	if ui.GetFocus() != right || kept != "" {
		t.Fatal("right did not select without applying")
	}
	key(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	if kept != "remote" {
		t.Fatal("Enter did not keep selected Notion version")
	}
	kept = ""
	left.GetMouseCapture()(tview.MouseLeftClick, tcell.NewEventMouse(5, 5, tcell.Button1, 0))
	if ui.GetFocus() != left || kept != "" {
		t.Fatal("click did not select local version")
	}
	key(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	if kept != "local" {
		t.Fatal("Enter did not keep clicked version")
	}
}
