package main

import (
	"fmt"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"ntty/internal/store"
)

func (a *app) revisions() {
	if a.modal {
		return
	}
	d := a.docs[a.active]
	if d == nil {
		a.message("Open a page to review its local revisions", false)
		return
	}
	id := d.Page.ID
	revisions, err := a.store.Revisions(id)
	if err != nil {
		a.message(err.Error(), true)
		return
	}
	list := tview.NewList().ShowSecondaryText(true).SetHighlightFullLine(true).
		SetMainTextStyle(tcell.StyleDefault).SetSecondaryTextStyle(quiet).SetSelectedStyle(selection)
	list.SetBorder(true).SetBorderStyle(quiet).SetTitle(fmt.Sprintf(" Snapshots · %d/%d ", len(revisions), store.MaxRevisions))
	preview := tview.NewTextView().SetWrap(true).SetWordWrap(true)
	preview.SetBorder(true).SetBorderStyle(quiet).SetTitle(" Selected snapshot ").SetBorderPadding(0, 0, 1, 1)
	current := tview.NewTextView().SetWrap(true).SetWordWrap(true)
	current.SetBorder(true).SetBorderStyle(quiet).SetBorderPadding(0, 0, 1, 1)
	updateCurrent := func() {
		latest := a.docs[id]
		if current.GetText(false) != latest.Text {
			current.SetText(latest.Text)
		}
		state := "saved"
		if latest.Dirty || latest.Pending != nil {
			state = "unsynced"
		}
		current.SetTitle(" Current draft · " + state + " ")
	}
	updateCurrent()
	list.SetChangedFunc(func(i int, _, _ string, _ rune) {
		if i >= 0 && i < len(revisions) {
			r := revisions[i]
			preview.SetText(r.Doc.Text).ScrollToBeginning()
			preview.SetTitle(" " + r.At.Local().Format("2006-01-02 15:04:05 MST") + " ")
			updateCurrent()
		}
	})
	for _, r := range revisions {
		state := "saved"
		if r.Doc.Dirty || r.Doc.Pending != nil {
			state = "draft"
		}
		list.AddItem(r.At.Local().Format("2006-01-02 15:04:05"), tview.Escape(r.Reason)+" · "+state, 0, nil)
	}
	if len(revisions) == 0 {
		preview.SetText("No local snapshots yet. Snapshots are kept on this device as you work.")
	}
	// Selecting a row reviews it; only the explicit restore button changes text.
	list.SetSelectedFunc(func(int, string, string, rune) { a.ui.SetFocus(preview) })
	restore := a.button("Restore as draft", func() {
		i := list.GetCurrentItem()
		if i < 0 || i >= len(revisions) {
			return
		}
		unchanged := a.docs[id] != nil && a.docs[id].Text == revisions[i].Doc.Text
		if err := a.restoreRevision(id, revisions[i]); err != nil {
			a.message("Restore: "+err.Error(), true)
			return
		}
		a.closeModal()
		a.ui.SetFocus(a.editor)
		if unchanged {
			a.message("Snapshot already matches the current draft", false)
		} else if a.docs[id].Dirty {
			a.message("Restored as a local draft · waiting for autosave…", false)
		} else {
			a.message("Restored locally · matches the current Notion base", false)
		}
	})
	restore.SetDisabled(len(revisions) == 0).SetDisabledStyle(quiet)
	close := a.button("Close", a.closeModal)
	buttons := tview.NewFlex().AddItem(restore, 0, 1, false).AddItem(close, 0, 1, false)
	previews := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(preview, 0, 1, false).AddItem(current, 0, 1, false)
	body := tview.NewFlex().AddItem(list, 0, 2, true).AddItem(previews, 0, 5, false)
	help := tview.NewTextView().SetText("↑↓ select · Enter preview · Tab switch pane · Esc close\nRestore saves a backup first, then follows normal autosave.").SetTextStyle(quiet)
	pane := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(body, 0, 1, true).AddItem(help, 2, 0, false).AddItem(buttons, 1, 0, false)
	pane.Box = tview.NewBox()
	pane.SetBorder(true).SetBorderStyle(quiet).SetTitle(" Local revisions · " + tview.Escape(d.Page.Title) + " ")
	pane.SetDrawFunc(func(_ tcell.Screen, _, _, _, _ int) (int, int, int, int) {
		updateCurrent()
		return pane.GetInnerRect()
	})
	focus := []tview.Primitive{list, preview, current, restore, close}
	if len(revisions) == 0 {
		focus = []tview.Primitive{list, preview, current, close}
	}
	pane.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyTab || e.Key() == tcell.KeyBacktab {
			step := 1
			if e.Key() == tcell.KeyBacktab {
				step = len(focus) - 1
			}
			for i, p := range focus {
				if p.HasFocus() {
					a.ui.SetFocus(focus[(i+step)%len(focus)])
					return nil
				}
			}
		}
		return e
	})
	a.overlay(pane, 112, 34)
}

// restoreRevision commits the backup and replacement draft before changing the
// editor. Snapshot metadata is for review only: the current sync base, fetch time,
// and retry backoff remain authoritative. An explicit restore clears an old
// sync pause and safely preflights the restored draft again.
func (a *app) restoreRevision(id string, revision store.Revision) error {
	d := a.docs[id]
	if d == nil || a.active != id || revision.Doc.Page.ID != id {
		return fmt.Errorf("the active page has changed; reopen local revisions")
	}
	if a.syncing() || a.fetching[id] {
		return fmt.Errorf("wait for the current sync or fetch before restoring")
	}
	if d.Text == revision.Doc.Text {
		return nil
	}
	if err := a.drafts.Flush(id); err != nil {
		return fmt.Errorf("save current draft: %w", err)
	}
	if err := a.store.SaveRevision(*d, "Before restore"); err != nil {
		return err
	}
	next := *d
	next.Text = revision.Doc.Text
	next.Dirty = next.Text != next.Base.Markdown
	next.Pending = nil
	a.drafts.Put(next)
	if err := a.drafts.Flush(id); err != nil {
		return fmt.Errorf("save restored draft: %w", err)
	}
	*d = next
	a.blocked[id] = false
	a.showDoc(d)
	// Restores and recovered drafts both follow the normal idle autosave path.
	a.changed[id] = time.Now()
	a.rebuildList()
	return nil
}
