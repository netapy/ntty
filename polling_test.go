package main

import (
	"errors"
	"testing"
	"time"

	"ntty/internal/notion"
	"ntty/internal/store"
)

func TestMetadataNoOpPreservesCachesAndDoesNotWrite(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := notion.Page{ID: "page", Title: "Page", Kind: "page", ParentKind: "workspace"}
	a := newApp(notion.NewDemo(nil), s, store.State{Pages: []notion.Page{p}}, nil, "", false)
	defer a.cancel()
	a.resolvedPaths = map[string][]notion.Page{p.ID: {p}}
	a.listCache["query"] = cachedList{}
	a.applyPageMetadata(p)
	if len(a.listCache) != 1 || len(a.resolvedPaths) != 1 || a.drafts.next != 0 {
		t.Fatal("unchanged metadata invalidated caches or wrote state")
	}
	p.Edited = "new edit"
	a.applyPageMetadata(p)
	if len(a.resolvedPaths) != 1 {
		t.Fatal("content edit invalidated ancestry")
	}
	p.ParentKind, p.ParentID = "page_id", "other"
	a.applyPageMetadata(p)
	if len(a.resolvedPaths) != 0 {
		t.Fatal("moved page retained stale ancestry")
	}
	if err := a.drafts.FlushAll(); err != nil {
		t.Fatal(err)
	}
}

func TestBackgroundBudgetCooldownAndActivity(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := notion.Page{ID: "page", Kind: "page"}
	a := newApp(notion.NewDemo(nil), s, store.State{}, nil, "", false)
	defer a.cancel()
	now := time.Now()
	a.active = p.ID
	a.docs[p.ID] = &notion.Doc{Page: p, Dirty: true}
	a.changed[p.ID] = now
	if !a.backgroundBusy(now) {
		t.Fatal("background work competed with dirty draft")
	}
	a.docs[p.ID].Dirty = false
	a.backgroundResult(errors.New("offline"))
	if !a.backgroundBusy(now) || a.backgroundUntil.Sub(now) < 14*time.Second {
		t.Fatal("no shared failure cooldown")
	}
	a.backgroundResult(errors.New("offline"))
	if a.backgroundUntil.Sub(now) < 29*time.Second {
		t.Fatal("failure backoff did not grow")
	}
	a.backgroundResult(nil)
	if a.backgroundBusy(time.Now()) {
		t.Fatal("successful read did not clear cooldown")
	}
	a.refreshDelay[p.ID] = time.Minute
	a.remoteChecked[p.ID] = now
	a.lastActivity = now.Add(-3 * time.Minute)
	a.touchActivity()
	if a.refreshDelay[p.ID] != 0 || !a.remoteChecked[p.ID].IsZero() {
		t.Fatal("returning to idle app did not request fresh validation")
	}
	a.nextMetadata = time.Now().Add(time.Minute)
	a.refreshPageMetadata(time.Now())
	if a.pageCheckBusy {
		t.Fatal("metadata exceeded global budget")
	}
}

func TestFreshWorkspaceAndCompleteBreadcrumbNeedNoRequests(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := notion.Page{ID: "page", Title: "Page", Kind: "page", ParentKind: "workspace"}
	b := &startupBackend{Backend: notion.NewDemo(nil), calls: make(chan string, 4)}
	a := newApp(b, s, store.State{Pages: []notion.Page{p}, WorkspaceIndexed: time.Now()}, nil, "", false)
	defer a.cancel()
	a.loadList("", "", false, false)
	a.setBreadcrumb(p)
	a.resolveBreadcrumb(p)
	if a.listing || a.breadcrumbCancel != nil {
		t.Fatal("cached workspace/path started background discovery")
	}
	select {
	case call := <-b.calls:
		t.Fatalf("unnecessary request: %s", call)
	default:
	}
}
