package main

import (
	"context"
	"errors"
	"time"

	"ntty/internal/notion"
)

const (
	syncIdleDelay         = 3 * time.Second
	remoteRefreshInterval = 5 * time.Second
)

// Sync has four orthogonal facts and no implicit retry state:
//   - Doc.Dirty: visible text differs from the last confirmed base.
//   - Doc.Pending: an ambiguous PATCH pair must be reconciled first.
//   - changed[id]: the 3-second idle/retry deadline starts here.
//   - blocked[id]: automatic sync is paused until Compare, restore, or Ctrl+S.
//
// savingPage is non-empty only while the single serialized request is running.
type syncRequest struct {
	snapshot notion.Doc
	base     notion.Content
	text     string
	draft    string
}

func (a *app) syncing() bool { return a.savingPage != "" }

func (a *app) needsSync(id string) bool {
	d := a.docs[id]
	return d != nil && (d.Dirty || d.Pending != nil)
}

// Keep the active clean page live with Notion. Dirty pages are never replaced:
// their next save performs the same remote preflight and merge instead.
func (a *app) autoRefresh(now time.Time) {
	if a.backgroundBusy(now) {
		return
	}
	d := a.docs[a.active]
	interval := max(remoteRefreshInterval, a.refreshDelay[a.active])
	if now.Sub(a.lastActivity) >= 2*time.Minute {
		interval = max(interval, 3*time.Minute)
	}
	if d == nil || d.Page.InTrash || d.Page.Kind != "page" || a.needsSync(d.Page.ID) || now.Sub(a.remoteChecked[d.Page.ID]) < interval {
		return
	}
	a.fetchContent(d.Page, true)
}

func (a *app) backgroundBusy(now time.Time) bool {
	if a.demo || a.modal || a.quitting || a.syncing() || a.creating || a.renaming || a.trashBusy || a.commentsSending() || a.fetchPage != "" || now.Before(a.backgroundUntil) {
		return true
	}
	for id := range a.changed {
		if a.needsSync(id) && !a.blocked[id] {
			return true
		}
	}
	return false
}

func (a *app) backgroundResult(err error) {
	if errors.Is(err, context.Canceled) {
		return
	}
	if err == nil {
		a.backgroundFailures = 0
		a.backgroundUntil = time.Time{}
		return
	}
	a.backgroundFailures = min(a.backgroundFailures+1, 6)
	a.backgroundUntil = time.Now().Add(min(15*time.Second<<(a.backgroundFailures-1), 5*time.Minute))
}

func (a *app) touchActivity() {
	now := time.Now()
	if now.Sub(a.lastActivity) >= 2*time.Minute {
		delete(a.refreshDelay, a.active)
		delete(a.remoteChecked, a.active)
	}
	a.lastActivity = now
}

func (a *app) onEdit() {
	if a.setting {
		return
	}
	d := a.docs[a.active]
	if d == nil {
		return
	}
	id := d.Page.ID
	wasDirty := d.Dirty
	d.Text = a.editor.GetText()
	d.Dirty = d.Text != d.Base.Markdown
	if d.Dirty || d.Pending != nil {
		a.changed[id] = time.Now()
	} else {
		delete(a.changed, id)
		a.blocked[id] = false
	}
	a.drafts.Put(*d)
	if a.blocked[id] {
		a.message("Draft updated · sync remains paused; Ctrl+S retries or compare with Notion", false)
	}
	if wasDirty != d.Dirty {
		a.rebuildList()
	}
}

func (a *app) autoSave(now time.Time) {
	if a.syncing() || a.quitting || a.trashBusy {
		return
	}
	// Pick the oldest eligible edit instead of relying on map iteration. A busy
	// page can therefore never starve another recovered draft.
	var next string
	var editedAt time.Time
	for id, changed := range a.changed {
		d := a.docs[id]
		if d == nil || !a.needsSync(id) || a.blocked[id] || now.Sub(changed) < syncIdleDelay {
			continue
		}
		if next == "" || changed.Before(editedAt) {
			next, editedAt = id, changed
		}
	}
	if next != "" {
		a.save(next, false)
	}
}

func (a *app) save(id string, manual bool) {
	d := a.docs[id]
	if d == nil {
		return
	}
	if d.Page.InTrash {
		a.message("Restore this page from Trash before syncing", true)
		return
	}
	if a.syncing() {
		if manual {
			a.message("Sync already in progress · latest typing is safe locally", false)
		}
		return
	}
	if a.blocked[id] {
		if !manual {
			return
		}
		// Ctrl+S is an explicit retry. The backend still performs a fresh
		// read/merge preflight, so clearing the UI pause cannot overwrite a
		// conflicting remote edit.
		a.blocked[id] = false
	}
	pending, reconciling := d.Pending, d.Pending != nil
	if !d.Dirty && !reconciling {
		delete(a.changed, id)
		a.message("All changes saved", false)
		return
	}
	if !reconciling && !d.Base.Editable() {
		a.blocked[id] = true
		a.rebuildList()
		a.message("Page incomplete · draft kept locally; Ctrl+K → Compare with Notion", true)
		return
	}
	if err := a.drafts.Flush(id); err != nil {
		a.message("Local draft failed: "+err.Error(), true)
		return
	}

	request := syncRequest{snapshot: *d, base: d.Base, text: d.Text, draft: d.Text}
	if reconciling {
		request.base, request.text, request.draft = pending.Base, pending.Text, pending.Draft
	}
	if err := a.store.SaveRevision(request.snapshot, "Before sync"); err != nil {
		a.message("Sync snapshot: "+err.Error(), true)
		return
	}
	a.savingPage = id
	// Invalidate an older background read before writing. Even if the backend
	// ignores cancellation, its completion cannot replace this newer save.
	if a.fetchPage == id {
		a.cancelFetch()
	}
	a.rebuildList()
	if reconciling {
		a.message("↥ Confirming previous Notion write…", false)
	} else {
		a.message("↥ Syncing to Notion…", false)
	}
	go func() {
		var content notion.Content
		var err error
		if backend, ok := a.backend.(interface {
			ReconcileSave(context.Context, string, string, string) (notion.Content, error)
		}); ok && reconciling {
			content, err = backend.ReconcileSave(a.ctx, id, request.base.Markdown, request.text)
		} else {
			content, err = a.backend.Save(a.ctx, id, request.base.Markdown, request.text)
		}
		a.ui.QueueUpdateDraw(func() { a.finishSync(id, request, content, err) })
	}()
}

func (a *app) finishSync(id string, request syncRequest, content notion.Content, err error) {
	a.savingPage = ""
	d := a.docs[id]
	if d == nil {
		return
	}
	if err != nil {
		a.finishSyncError(id, request, err)
	} else {
		a.finishSyncSuccess(id, request, content)
	}
	a.rebuildList()
	if a.quitting {
		a.quit()
	}
}

func (a *app) finishSyncError(id string, request syncRequest, err error) {
	var attempt *notion.SaveAttemptError
	if errors.As(err, &attempt) {
		d := a.docs[id]
		d.Pending = &notion.PendingSave{Base: attempt.Base, Text: attempt.Text, Draft: request.draft}
		a.drafts.Put(*d)
		if cacheErr := a.drafts.Flush(id); cacheErr != nil {
			a.blocked[id] = true
			a.message("Ambiguous Notion write, and its local checkpoint failed: "+cacheErr.Error(), true)
			return
		}
	}
	needsAttention := errors.Is(err, notion.ErrConflict) || errors.Is(err, notion.ErrUnsafeUpdate)
	a.blocked[id] = needsAttention
	if needsAttention {
		switch {
		case errors.Is(err, notion.ErrProtectedContent):
			a.message("This draft changes protected Notion content · draft kept; Ctrl+R can restore a safe snapshot", true)
		case errors.Is(err, notion.ErrUnsafeUpdate):
			a.message("Notion could not safely target this edit · draft kept locally", true)
		case errors.Is(err, notion.ErrIncomplete):
			a.message("Notion returned an incomplete page · draft kept locally", true)
		default:
			a.message("Sync paused · local draft is safe · Ctrl+K → Compare with Notion", true)
		}
		return
	}
	// One timestamp drives both idle saves and retries. There is no second
	// throttle that can silently turn the promised 3-second autosave into 10s.
	a.changed[id] = time.Now()
	a.message("Sync delayed · retrying automatically", false)
}

func (a *app) finishSyncSuccess(id string, request syncRequest, content notion.Content) {
	d := a.docs[id]
	now := time.Now()
	synced := request.snapshot
	synced.Base, synced.Text, synced.Dirty, synced.Fetched = content, content.Markdown, false, now
	synced.Pending = nil
	revisionErr := a.store.SaveRevision(synced, "Synced")

	latest := d.Text
	rebased, ok, rebaseErr := notion.RebaseDraft(request.draft, latest, content.Markdown)
	d.Pending = nil
	if rebaseErr != nil || !ok {
		// The confirmed remote result and the latest local draft both remain
		// available. Keep the old base on disk so even a restart must preflight
		// against the changed remote page instead of treating remote text as a
		// local deletion.
		d.Dirty = d.Text != d.Base.Markdown
		a.blocked[id] = true
		a.drafts.Put(*d)
		if cacheErr := a.drafts.Flush(id); cacheErr != nil {
			a.message("Sync paused, but preserving its local checkpoint failed: "+cacheErr.Error(), true)
		} else {
			a.message("Sync paused · newer typing overlaps a Notion change; both versions are safe", true)
		}
		return
	}

	d.Base = content
	d.Text = rebased
	d.Dirty = d.Text != content.Markdown
	d.Fetched = now
	a.remoteChecked[id] = now
	delete(a.refreshDelay, id)
	a.backgroundResult(nil)
	a.blocked[id] = false
	if d.Dirty {
		if a.changed[id].IsZero() {
			a.changed[id] = now
		}
	} else {
		delete(a.changed, id)
	}
	if a.active == id {
		a.updateEditorAfterSync(rebased)
	}
	a.drafts.Put(*d)
	if cacheErr := a.drafts.Flush(id); cacheErr != nil {
		a.blocked[id] = true
		a.message("Synced, but local cache failed: "+cacheErr.Error(), true)
	} else if revisionErr != nil {
		a.message("Synced, but revision snapshot failed: "+revisionErr.Error(), true)
	} else if d.Dirty {
		a.message("Synced earlier edits · newer typing was safely rebased", false)
	} else if content.Normalized {
		a.message("Saved · Notion normalized Markdown", false)
	} else {
		a.message("✓ Saved to Notion · "+now.Format("15:04:05"), false)
	}
}

func (a *app) updateEditorAfterSync(text string) {
	if a.editor.GetText() == text {
		return
	}
	a.setting = true
	defer func() { a.setting = false }()
	if a.editor.Rebase(text) {
		return
	}
	oldVisible := a.editor.visible
	view := a.editor.snapshot()
	a.editor.SetText(text, false)
	a.editor.anchor = mapTextOffset(oldVisible, a.editor.visible, view.anchor)
	a.editor.head = mapTextOffset(oldVisible, a.editor.visible, view.head)
	a.editor.visualRow = -1
	a.editor.syncNativeSelection()
	a.editor.SetOffset(view.row, 0)
}
