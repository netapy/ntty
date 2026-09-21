package main

import (
	"errors"
	"testing"
	"time"

	"ntty/internal/notion"
	"ntty/internal/store"
)

func syncTestApp(t *testing.T, doc notion.Doc) *app {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveDoc(doc); err != nil {
		t.Fatal(err)
	}
	a := newApp(notion.NewDemo(nil), s, store.State{}, []notion.Doc{doc}, "", true)
	t.Cleanup(a.cancel)
	a.active = doc.Page.ID
	a.showDoc(a.docs[doc.Page.ID])
	return a
}

func TestSyncCompletionRebasesTypingMadeInFlight(t *testing.T) {
	base := notion.Content{Object: "page_markdown", ID: "p", Markdown: "First\nMiddle\nLast"}
	submitted := notion.Doc{Page: notion.Page{ID: "p", Kind: "page"}, Base: base, Text: "First saved\nMiddle\nLast", Dirty: true}
	latest := submitted
	latest.Text = "First saved\nMiddle\nLast typed"
	a := syncTestApp(t, latest)
	a.changed["p"] = time.Now().Add(-time.Second)
	request := syncRequest{snapshot: submitted, base: base, text: submitted.Text, draft: submitted.Text}
	confirmed := notion.Content{Object: "page_markdown", ID: "p", Markdown: "First saved\nMiddle remote\nLast"}

	a.finishSyncSuccess("p", request, confirmed)
	want := "First saved\nMiddle remote\nLast typed"
	d := a.docs["p"]
	if d.Text != want || d.Base.Markdown != confirmed.Markdown || !d.Dirty || a.blocked["p"] {
		t.Fatalf("newer typing was not rebased safely: %+v", d)
	}
	if a.editor.GetText() != want {
		t.Fatalf("editor was not updated with rebased text: %q", a.editor.GetText())
	}
	disk, err := a.store.LoadDoc("p")
	if err != nil || disk.Text != d.Text || disk.Base.Markdown != d.Base.Markdown || disk.Dirty != d.Dirty {
		t.Fatalf("rebased draft was not durable: %+v, %v", disk, err)
	}
}

func TestAmbiguousWriteReconcilesExactPairBeforeNewerTyping(t *testing.T) {
	base := notion.Content{Object: "page_markdown", ID: "p", Markdown: "Before\nMiddle\nLast"}
	submitted := notion.Doc{Page: notion.Page{ID: "p", Kind: "page"}, Base: base, Text: "Before\nMiddle\nLocal", Dirty: true}
	latest := submitted
	latest.Text = "Before\nMiddle\nLocal newer"
	a := syncTestApp(t, latest)
	request := syncRequest{snapshot: submitted, base: base, text: submitted.Text, draft: submitted.Text}
	attemptBase := notion.Content{Object: "page_markdown", ID: "p", Markdown: "Remote\nMiddle\nLast"}
	attemptText := "Remote\nMiddle\nLocal"

	a.finishSyncError("p", request, &notion.SaveAttemptError{Err: errors.New("503 response lost"), Base: attemptBase, Text: attemptText})
	pending := a.docs["p"].Pending
	if pending == nil || pending.Base.Markdown != attemptBase.Markdown || pending.Text != attemptText || pending.Draft != submitted.Text {
		t.Fatalf("ambiguous write pair was not retained exactly: %+v", pending)
	}
	if d := a.docs["p"]; d.Base.Markdown != base.Markdown || d.Text != latest.Text {
		t.Fatalf("ambiguous write replaced the visible draft: %+v", d)
	}

	retry := syncRequest{snapshot: *a.docs["p"], base: pending.Base, text: pending.Text, draft: pending.Draft}
	confirmed := notion.Content{Object: "page_markdown", ID: "p", Markdown: attemptText}
	a.finishSyncSuccess("p", retry, confirmed)
	d := a.docs["p"]
	if d.Text != "Remote\nMiddle\nLocal newer" || d.Base.Markdown != attemptText || !d.Dirty || a.blocked["p"] {
		t.Fatalf("new typing was not applied after exact reconciliation: %+v", d)
	}
}

func TestOverlappingSyncCompletionFailsClosed(t *testing.T) {
	base := notion.Content{Object: "page_markdown", ID: "p", Markdown: "Base"}
	submitted := notion.Doc{Page: notion.Page{ID: "p", Kind: "page"}, Base: base, Text: "Submitted", Dirty: true}
	latest := submitted
	latest.Text = "Newest local"
	a := syncTestApp(t, latest)
	request := syncRequest{snapshot: submitted, base: base, text: submitted.Text, draft: submitted.Text}
	confirmed := notion.Content{Object: "page_markdown", ID: "p", Markdown: "Different Notion text"}

	a.finishSyncSuccess("p", request, confirmed)
	d := a.docs["p"]
	if d.Text != latest.Text || d.Base.Markdown != base.Markdown || !d.Dirty || !a.blocked["p"] {
		t.Fatalf("overlap did not preserve the old safe base and local draft: %+v blocked=%v", d, a.blocked["p"])
	}
	disk, err := a.store.LoadDoc("p")
	if err != nil || disk.Text != latest.Text || disk.Base.Markdown != base.Markdown {
		t.Fatalf("fail-closed draft was not durable: %+v, %v", disk, err)
	}
}

func TestIncompleteRemoteResponseRetriesAutomatically(t *testing.T) {
	base := notion.Content{Object: "page_markdown", ID: "p", Markdown: "Base"}
	doc := notion.Doc{Page: notion.Page{ID: "p", Kind: "page"}, Base: base, Text: "Draft", Dirty: true}
	a := syncTestApp(t, doc)
	before := time.Now()
	a.finishSyncError("p", syncRequest{snapshot: doc, base: base, text: doc.Text, draft: doc.Text}, notion.ErrIncomplete)
	if a.blocked["p"] || a.changed["p"].Before(before) || a.docs["p"].Text != doc.Text {
		t.Fatal("temporary incomplete response paused sync or lost the draft")
	}
}
