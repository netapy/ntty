package main

import (
	"github.com/rivo/tview"
	"ntty/internal/notion"
	"strings"
	"time"
)

func (a *app) renamePageDialog(page notion.Page) {
	backend, ok := a.backend.(notion.RenameBackend)
	if !ok || a.renaming {
		return
	}
	form := tview.NewForm().SetButtonStyle(accent).SetButtonActivatedStyle(selection)
	form.AddInputField("Name", page.Title, 42, nil, nil)
	form.AddButton("Rename", func() {
		title := strings.TrimSpace(form.GetFormItem(0).(*tview.InputField).GetText())
		if title == "" {
			return
		}
		a.closeModal()
		a.renaming = true
		go func() {
			updated, err := backend.Rename(a.ctx, page, title)
			a.ui.QueueUpdateDraw(func() {
				a.renaming = false
				if err != nil {
					a.message(err.Error(), true)
				} else {
					a.applyPageMetadata(updated)
					a.message("Page renamed", false)
				}
				if a.quitting {
					a.quit()
				}
			})
		}()
	}).AddButton("Cancel", a.closeModal)
	form.SetBorder(true).SetTitle(" Rename page ")
	a.overlay(form, 60, 7)
}

// Check one page at a time through the existing rate-limited client. Prefer the
// active page, then visible sidebar entries; never infer deletion from absence
// in a partial search response or from a transport/permission error.
func (a *app) refreshPageMetadata(now time.Time) {
	if a.backgroundBusy(now) || a.listing || a.pageCheckBusy || now.Before(a.nextMetadata) {
		return
	}
	if a.pageChecks == nil {
		a.pageChecks = map[string]time.Time{}
	}
	candidates := append([]notion.Page{}, a.favoritePages...)
	candidates = append(candidates, a.recentPages...)
	candidates = append(candidates, a.visible...)
	if d := a.docs[a.active]; d != nil {
		candidates = append([]notion.Page{d.Page}, candidates...)
	}
	for _, p := range candidates {
		interval := 10 * time.Minute
		if p.ID == a.active {
			interval = time.Minute
		}
		id := canonicalID(p.ID)
		if p.Kind != "page" || p.InTrash || now.Sub(a.pageChecks[id]) < interval {
			continue
		}
		a.pageChecks[id] = now
		// ponytail: one global budget, rather than a request scheduler.
		a.nextMetadata = now.Add(15 * time.Second)
		a.pageCheckBusy = true
		go func() {
			updated, err := a.backend.Page(a.ctx, p.ID)
			a.ui.QueueUpdateDraw(func() {
				a.pageCheckBusy = false
				a.backgroundResult(err)
				if err == nil {
					a.applyPageMetadata(updated)
				}
			})
		}()
		return
	}
}

func (a *app) applyPageMetadata(page notion.Page) {
	if page.ID == "" {
		return
	}
	old, known := a.knownPage(page.ID)
	if known && old == page {
		return
	}
	a.state.Pages = mergePages([]notion.Page{page}, a.state.Pages)
	for _, pages := range []*[]notion.Page{&a.state.Pages, &a.state.Pins, &a.state.Recents, &a.listed, &a.back, &a.forward} {
		for i, p := range *pages {
			if p.ID == page.ID {
				(*pages)[i] = page
			}
		}
	}
	if d := a.docs[page.ID]; d != nil {
		d.Page = page
		if page.InTrash {
			a.blocked[page.ID] = true
			delete(a.changed, page.ID)
		}
		a.drafts.Put(*d)
	}
	for key := range a.listCache {
		delete(a.listCache, key)
	}
	if !known || old.ParentID != page.ParentID || old.ParentKind != page.ParentKind || old.Title != page.Title || old.InTrash != page.InTrash {
		for id, path := range a.resolvedPaths {
			for _, ancestor := range path {
				if canonicalID(ancestor.ID) == canonicalID(page.ID) {
					delete(a.resolvedPaths, id)
					break
				}
			}
		}
		if a.breadcrumbCancel != nil {
			a.breadcrumbCancel()
		}
	}
	a.persistState()
	if page.ID == a.active {
		a.setTitle(page.Title)
		a.setBreadcrumb(page)
		a.editor.SetDisabled(page.InTrash)
		if page.InTrash {
			a.cancelFetch()
			a.message("Page moved to Trash · local draft retained · page menu can restore it", true)
		}
	}
	a.rebuildList()
}

func (a *app) setPageTrash(page notion.Page, trash bool) {
	backend, ok := a.backend.(notion.TrashBackend)
	if !ok {
		return
	}
	if a.syncing() || a.trashBusy {
		a.message("Wait for the current write to finish", false)
		return
	}
	if d := a.docs[page.ID]; d != nil {
		if err := a.drafts.Flush(page.ID); err != nil {
			a.message(err.Error(), true)
			return
		}
		if err := a.store.SaveRevision(*d, "Before changing Trash state"); err != nil {
			a.message(err.Error(), true)
			return
		}
	}
	a.trashBusy = true
	go func() {
		updated, err := backend.SetTrash(a.ctx, page.ID, trash)
		a.ui.QueueUpdateDraw(func() {
			a.trashBusy = false
			defer func() {
				if a.quitting {
					a.quit()
				}
			}()
			if err != nil {
				a.message(err.Error(), true)
				return
			}
			a.applyPageMetadata(updated)
			if !trash {
				a.blocked[page.ID] = false
				if a.needsSync(page.ID) {
					a.changed[page.ID] = time.Now()
				}
				a.open(updated)
			}
		})
	}()
}

func (a *app) openTrash() {
	if a.modal {
		return
	}
	list := tview.NewList().ShowSecondaryText(false).SetSelectedStyle(navigationSelection)
	for _, p := range a.state.Pages {
		if p.InTrash {
			page := p
			list.AddItem(tview.Escape(page.Title), "", 0, func() { a.closeModal(); a.setPageTrash(page, false) })
		}
	}
	if list.GetItemCount() == 0 {
		list.AddItem("No known pages in Trash", "", 0, nil)
	}
	list.AddItem("Close", "", 0, a.closeModal)
	list.SetBorder(true).SetTitle(" Trash · Enter restores ")
	a.overlay(list, 60, min(20, list.GetItemCount()+2))
}
