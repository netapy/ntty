package main

import (
	"context"
	"encoding/json"
	"ntty/internal/notion"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLiveRecoverCapturedDraft(t *testing.T) {
	if os.Getenv("NTTY_RECOVER_CAPTURED") != "1" {
		t.Skip("opt-in live draft recovery")
	}
	data, err := os.ReadFile(os.Getenv("NTTY_CAPTURED_DOC"))
	if err != nil {
		t.Fatal(err)
	}
	var doc notion.Doc
	if err = json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	content, err := notion.NewClient().Save(ctx, doc.Page.ID, doc.Base.Markdown, doc.Text)
	if err != nil {
		t.Fatalf("actual draft save: %v", err)
	}
	t.Logf("saved captured draft: %d bytes normalized=%v", len(content.Markdown), content.Normalized)
}

// Opt-in, read-only reproduction against a real locally captured page. No API
// writes or changes to the user's profile are made by this diagnostic.
func TestCapturedPageEditingAndRebase(t *testing.T) {
	path := os.Getenv("NTTY_CAPTURED_DOC")
	if path == "" {
		t.Skip("set NTTY_CAPTURED_DOC and NTTY_CAPTURED_REMOTE")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc notion.Doc
	if err = json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(os.Getenv("NTTY_CAPTURED_REMOTE"))
	if err != nil {
		t.Fatal(err)
	}
	var remote notion.Content
	if err = json.Unmarshal(data, &remote); err != nil {
		t.Fatal(err)
	}
	t.Logf("base editable=%v remote editable=%v dirty=%v", doc.Base.Editable(), remote.Editable(), doc.Dirty)
	start := time.Now()
	merged, ok, err := notion.RebaseDraft(doc.Base.Markdown, doc.Text, remote.Markdown)
	t.Logf("rebase ok=%v err=%v equals remote=%v elapsed=%v", ok, err, merged == remote.Markdown, time.Since(start))
	if !ok || err != nil {
		t.Errorf("actual draft cannot reconcile: %v", err)
	}
	r := newRichEditor()
	r.SetText(remote.Markdown, false)
	if richMarkdown(r.lines) != remote.Markdown {
		t.Error("source did not round trip")
	}
	start = time.Now()
	r.Replace(0, 0, "ntty diagnostic")
	t.Logf("typing took %v", time.Since(start))
	if r.GetText() == remote.Markdown {
		t.Error("typing at the start of actual page was rejected")
	}
	r.SetText(remote.Markdown, false)
	end := strings.Index(r.visible, "▦ Tasks")
	if end < 0 {
		t.Fatal("database reference not found")
	}
	r.Replace(0, end, "")
	if r.GetText() == remote.Markdown {
		t.Error("bulk deletion before database was rejected")
	}
}
