package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"ntty/internal/notion"
)

func TestRevisionsSurviveRestartWithoutChangingDraft(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	doc := notion.Doc{
		Page: notion.Page{ID: "page-one", Title: "Notes", Kind: "page"},
		Base: notion.Content{ID: "page-one", Object: "page_markdown", Markdown: "original", Truncated: true, Unknown: []string{"unknown-block"}},
		Text: "# Édité\n- [ ] task\n", Dirty: true,
		Fetched: time.Date(2026, 9, 20, 9, 30, 0, 0, time.UTC),
	}
	if err := s.SaveDoc(doc); err != nil {
		t.Fatal(err)
	}
	before := time.Now().UTC()
	if err := s.SaveRevision(doc, "Before sync"); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(s.Dir)
	if err != nil {
		t.Fatal(err)
	}
	revisions, err := reopened.Revisions(doc.Page.ID)
	if err != nil || len(revisions) != 1 {
		t.Fatalf("recovered revisions: %+v, %v", revisions, err)
	}
	r := revisions[0]
	if !reflect.DeepEqual(r.Doc, doc) || r.Reason != "Before sync" || r.At.Before(before) || r.At.After(time.Now()) {
		t.Fatalf("snapshot lost original document metadata: %+v", r)
	}
	// Returned values and later mutations must not alter an immutable snapshot.
	revisions[0].Doc.Base.Unknown[0] = "changed"
	revisions[0].Doc.Text = "changed"
	disk, err := reopened.LoadDoc(doc.Page.ID)
	if err != nil || !reflect.DeepEqual(disk, doc) {
		t.Fatalf("saving a revision changed the document: %+v, %v", disk, err)
	}
	reread, err := reopened.Revisions(doc.Page.ID)
	if err != nil || len(reread) != 1 || !reflect.DeepEqual(reread[0].Doc, doc) {
		t.Fatalf("snapshot was mutable: %+v, %v", reread, err)
	}
	for path, want := range map[string]os.FileMode{
		filepath.Join(s.Dir, "revisions"):                  0700,
		filepath.Join(s.Dir, "revisions", "page-one.json"): 0600,
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != want {
			t.Errorf("%s permissions = %o, want %o", path, info.Mode().Perm(), want)
		}
	}
}

func TestRevisionsDeduplicateOnlyConsecutiveTextAndKeepNewest(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	doc := notion.Doc{Page: notion.Page{ID: "history"}, Text: "first", Dirty: true}
	if err := s.SaveRevision(doc, "Draft"); err != nil {
		t.Fatal(err)
	}
	path, _ := s.revisionsPath(doc.Page.ID)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc.Dirty = false
	doc.Base.Markdown = doc.Text
	doc.Fetched = time.Now()
	if err := s.SaveRevision(doc, "Synced"); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(after, original) {
		t.Fatalf("duplicate text rewrote immutable history: %s, %v", after, err)
	}
	for i := 0; i < MaxRevisions+10; i++ {
		doc.Text = fmt.Sprintf("edit-%02d", i)
		if err := s.SaveRevision(doc, "Before sync"); err != nil {
			t.Fatal(err)
		}
	}
	revisions, err := s.Revisions(doc.Page.ID)
	if err != nil || len(revisions) != MaxRevisions {
		t.Fatalf("history bound: %d, %v", len(revisions), err)
	}
	for i, revision := range revisions {
		want := fmt.Sprintf("edit-%02d", MaxRevisions+9-i)
		if revision.Doc.Text != want {
			t.Errorf("revision %d = %q, want %q", i, revision.Doc.Text, want)
		}
		if i > 0 && revision.At.After(revisions[i-1].At) {
			t.Error("revisions are not newest first")
		}
	}
	// Revisit the oldest text: its fresh backup must survive the next eviction.
	doc.Text = revisions[len(revisions)-1].Doc.Text
	if err := s.SaveRevision(doc, "Before restore"); err != nil {
		t.Fatal(err)
	}
	doc.Text = "replacement"
	if err := s.SaveRevision(doc, "Restored"); err != nil {
		t.Fatal(err)
	}
	revisions, err = s.Revisions(doc.Page.ID)
	if err != nil || len(revisions) != MaxRevisions || revisions[1].Doc.Text != "edit-10" || revisions[1].Reason != "Before restore" {
		t.Fatalf("revisited text lost its new backup: %+v, %v", revisions, err)
	}
	other := notion.Doc{Page: notion.Page{ID: "other-page"}, Text: "replacement"}
	if err := s.SaveRevision(other, "Fetched"); err != nil {
		t.Fatal(err)
	}
	separate, err := s.Revisions(other.Page.ID)
	if err != nil || len(separate) != 1 || separate[0].Doc.Page.ID != other.Page.ID {
		t.Fatalf("histories leaked between pages: %+v, %v", separate, err)
	}
}

func TestRevisionsRejectInvalidIDsAndKeepCorruptHistory(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if revisions, err := s.Revisions("missing"); err != nil || len(revisions) != 0 {
		t.Fatalf("missing history: %+v, %v", revisions, err)
	}
	for _, id := range []string{"", "../escape", "a/b", "a\\b", ".", "/absolute", "has space"} {
		if _, err := s.Revisions(id); err == nil {
			t.Errorf("read accepted invalid ID %q", id)
		}
		if err := s.SaveRevision(notion.Doc{Page: notion.Page{ID: id}}, "Draft"); err == nil {
			t.Errorf("write accepted invalid ID %q", id)
		}
	}
	doc := notion.Doc{Page: notion.Page{ID: "corrupt"}, Text: "safe draft"}
	if err := s.SaveRevision(doc, " \n"); err == nil {
		t.Fatal("empty reason accepted")
	}
	if err := os.MkdirAll(filepath.Join(s.Dir, "revisions"), 0700); err != nil {
		t.Fatal(err)
	}
	path, _ := s.revisionsPath(doc.Page.ID)
	valid := Revision{At: time.Now().UTC(), Reason: "Fetched", Doc: doc}
	wrongPage := valid
	wrongPage.Doc.Page.ID = "different-page"
	wrongPageJSON, _ := json.Marshal([]Revision{wrongPage})
	tooMany := make([]Revision, MaxRevisions+1)
	for i := range tooMany {
		tooMany[i] = valid
	}
	tooManyJSON, _ := json.Marshal(tooMany)
	for _, data := range [][]byte{
		[]byte("{truncated"), []byte("null"), []byte("{}"), []byte("[null]"),
		[]byte("[{\"reason\":\"Fetched\"}]"), wrongPageJSON, tooManyJSON,
	} {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Revisions(doc.Page.ID); err == nil {
			t.Errorf("corrupt history accepted: %s", data)
		}
		if err := s.SaveRevision(doc, "Before restore"); err == nil {
			t.Errorf("corrupt history replaced: %s", data)
		}
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(after, data) {
			t.Fatalf("failed save changed corrupt history: %s, %v", after, err)
		}
	}
}

func TestRevisionsSerializeConcurrentBackups(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const count = 20
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			other := &Store{Dir: s.Dir}
			doc := notion.Doc{Page: notion.Page{ID: "concurrent"}, Text: fmt.Sprint(i)}
			if err := other.SaveRevision(doc, "Backup"); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	revisions, err := s.Revisions("concurrent")
	if err != nil || len(revisions) != count {
		t.Fatalf("concurrent backups lost: %d, %v", len(revisions), err)
	}
	seen := map[string]bool{}
	for _, r := range revisions {
		seen[r.Doc.Text] = true
	}
	if len(seen) != count {
		t.Fatalf("concurrent backup texts lost: %v", seen)
	}
}
