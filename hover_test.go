package main

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func TestHoverApplicationDoesNotStealFocusOrContinuouslyRedraw(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	ui := tview.NewApplication().SetScreen(screen).EnableMouse(true)
	screen.SetSize(40, 12)
	list := tview.NewList().ShowSecondaryText(false)
	list.AddItem("First", "", 0, nil).AddItem("Second", "", 0, nil)
	editor := tview.NewTextArea().SetText("Keep this selection", false)
	root := tview.NewFlex().AddItem(list, 20, 0, false).AddItem(editor, 0, 1, true)
	var draws atomic.Int32
	ui.SetRoot(root, true).SetFocus(editor).SetAfterDrawFunc(func(tcell.Screen) { draws.Add(1) })
	installHover(ui, root)
	done := make(chan error, 1)
	go func() { done <- ui.Run() }()
	defer func() {
		ui.Stop()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	await := func(check func() bool) {
		t.Helper()
		until := time.Now().Add(2 * time.Second)
		for time.Now().Before(until) {
			ok := false
			ui.QueueUpdate(func() { ok = check() })
			if ok {
				return
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatal("hover did not reach expected state")
	}
	await(func() bool { return draws.Load() > 0 })
	ui.QueueUpdate(func() { editor.Select(0, 4) })
	ui.QueueEvent(tcell.NewEventMouse(3, 1, tcell.ButtonNone, tcell.ModNone))
	await(func() bool {
		_, _, style, _ := screen.GetContent(3, 1)
		_, _, attrs := style.Decompose()
		return attrs&tcell.AttrBold != 0 && attrs&tcell.AttrUnderline == 0
	})
	ui.QueueUpdate(func() {
		selected, _, _ := editor.GetSelection()
		if selected != "Keep" || ui.GetFocus() != editor || list.GetCurrentItem() != 0 {
			t.Error("mouse hover altered focus or selection")
		}
	})
	baseline := draws.Load()
	ui.QueueEvent(tcell.NewEventMouse(4, 1, tcell.ButtonNone, tcell.ModNone))
	// Give the real event loop time to handle movement within the same row.
	time.Sleep(30 * time.Millisecond)
	ui.QueueUpdate(func() {
		if draws.Load() != baseline {
			t.Error("same hover row redrew unnecessarily")
		}
	})
	ui.QueueEvent(tcell.NewEventMouse(4, 4, tcell.ButtonNone, tcell.ModNone))
	await(func() bool {
		_, _, style, _ := screen.GetContent(3, 1)
		_, _, attrs := style.Decompose()
		return attrs&tcell.AttrBold == 0
	})
}

func TestHoverPreservesSelectionAndHandlesScrolledRows(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(40, 12)
	for _, secondary := range []bool{false, true} {
		list := tview.NewList().ShowSecondaryText(secondary)
		for i := 0; i < 10; i++ {
			sub := ""
			if secondary {
				sub = "metadata"
			}
			list.AddItem("café 界", sub, 0, nil)
		}
		list.SetRect(2, 2, 20, 6)
		list.SetCurrentItem(4)
		list.SetOffset(3, 0)
		changes := 0
		list.SetChangedFunc(func(int, string, string, rune) { changes++ })
		list.Draw(screen)
		hit := hoverAt(list, 5, 2)
		if hit.x != 2 || hit.y != 2 || hit.width != 20 {
			t.Fatalf("scrolled hit: %+v", hit)
		}
		wantHeight := 1
		if secondary {
			wantHeight = 2
		}
		if hit.height != wantHeight {
			t.Fatalf("row height: %+v", hit)
		}
		hit.paint(screen)
		_, _, style, _ := screen.GetContent(2, 2)
		_, _, attr := style.Decompose()
		if attr&tcell.AttrBold == 0 || attr&tcell.AttrUnderline != 0 {
			t.Fatal("hover was not painted")
		}
		_, _, blank, _ := screen.GetContent(18, 2)
		_, _, attrs := blank.Decompose()
		if attrs&(tcell.AttrBold|tcell.AttrUnderline|tcell.AttrReverse) != 0 {
			t.Fatal("hover highlighted empty row space")
		}
		if list.GetCurrentItem() != 4 || changes != 0 || list.HasFocus() {
			t.Fatal("hover changed selection/focus or fired callback")
		}
		list.SetOffset(9, 0)
		if empty := hoverAt(list, 5, 2+wantHeight); empty.width != 0 {
			t.Fatalf("hovered blank list space: %+v", empty)
		}
	}
}

func TestHoverRespectsModalAndDisabledControls(t *testing.T) {
	button := tview.NewButton("Run")
	button.SetRect(0, 0, 30, 10)
	if hoverAt(button, 2, 2).width == 0 {
		t.Fatal("button not hoverable")
	}
	button.SetDisabled(true)
	if hoverAt(button, 2, 2).width != 0 {
		t.Fatal("disabled button hoverable")
	}
	button.SetDisabled(false)
	modal := tview.NewFlex()
	modal.SetRect(0, 0, 30, 10)
	layers := tview.NewPages().AddPage("main", button, false, true).AddPage("modal", modal, false, true)
	layers.SetRect(0, 0, 30, 10)
	if hoverAt(layers, 2, 2).width != 0 {
		t.Fatal("hover leaked through modal padding")
	}
	label := tview.NewTextView().SetText("Status")
	label.SetRect(0, 0, 20, 1)
	if hoverAt(label, 2, 0).width != 0 {
		t.Fatal("ordinary label hoverable")
	}
	label.SetMouseCapture(func(a tview.MouseAction, e *tcell.EventMouse) (tview.MouseAction, *tcell.EventMouse) { return a, e })
	if hoverAt(label, 2, 0).width == 0 {
		t.Fatal("text action not hoverable")
	}
}

func TestHoverNeverUnderlinesKeyboardSelection(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	oldSelection, oldNavigation := selection, navigationSelection
	selection = tcell.StyleDefault.Reverse(true)
	navigationSelection = selection
	defer func() { selection, navigationSelection = oldSelection, oldNavigation }()
	list := tview.NewList().ShowSecondaryText(false).SetSelectedStyle(navigationSelection)
	list.AddItem("Selected", "", 0, nil).SetRect(0, 0, 20, 2)
	list.Focus(nil)
	list.Draw(screen)
	hoverAt(list, 2, 0).paint(screen)
	_, _, style, _ := screen.GetContent(2, 0)
	_, _, attributes := style.Decompose()
	if attributes&tcell.AttrUnderline != 0 {
		t.Fatal("hover added an underline to the selected row")
	}
}
