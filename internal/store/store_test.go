package store

import (
	"ntty/internal/notion"
	"os"
	"path/filepath"
	"testing"
)

func TestDraftSurvivesRestartAndRejectsTraversal(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	d := notion.Doc{Page: notion.Page{ID: "test-page", Title: "Notes"}, Base: notion.Content{Markdown: "old"}, Text: "Édité\n- [ ] task\n", Dirty: true}
	if err := s.SaveDoc(d); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(s.Dir)
	if err != nil {
		t.Fatal(err)
	}
	drafts, err := reopened.Drafts()
	if err != nil || len(drafts) != 1 || drafts[0].Text != d.Text || drafts[0].Base.Markdown != "old" {
		t.Fatalf("draft recovery: %+v %v", drafts, err)
	}
	info, err := os.Stat(filepath.Join(s.Dir, "pages", "test-page.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("draft permissions: %o", info.Mode().Perm())
	}
	d.Page.ID = "../../escape"
	if err := s.SaveDoc(d); err == nil {
		t.Fatal("path traversal accepted")
	}
}

func TestAmbiguousSaveCheckpointSurvivesRestartEvenWhenTextMatchesBase(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	base := notion.Content{Object: "page_markdown", Markdown: "base"}
	doc := notion.Doc{
		Page: notion.Page{ID: "pending", Kind: "page"}, Base: base, Text: "base",
		Pending: &notion.PendingSave{Base: base, Text: "attempted", Draft: "draft"},
	}
	if err := s.SaveDoc(doc); err != nil {
		t.Fatal(err)
	}
	drafts, err := s.Drafts()
	if err != nil || len(drafts) != 1 || drafts[0].Pending == nil || drafts[0].Pending.Text != "attempted" {
		t.Fatalf("pending save checkpoint was not recovered: %+v, %v", drafts, err)
	}
}
