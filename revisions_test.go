package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"ntty/internal/notion"
	"ntty/internal/store"
)

func revisionTestApp(t *testing.T) (*app, notion.Doc, store.Revision) {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	doc := notion.Doc{
		Page: notion.Page{ID: "revision-page", Title: "Revision test", Kind: "page"},
		Base: notion.Content{Object: "page_markdown", ID: "revision-page", Markdown: "Latest Notion base"},
		Text: "# Current draft [blue]literal", Dirty: true,
		Fetched: time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC),
	}
	old := doc
	old.Base = notion.Content{Object: "page_markdown", ID: doc.Page.ID, Markdown: "An older base", Truncated: true, Unknown: []string{"old-block"}}
	old.Text, old.Dirty = "# Previous snapshot [red]literal", false
	old.Fetched = doc.Fetched.Add(-24 * time.Hour)
	if err := s.SaveRevision(old, "Fetched"); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveDoc(doc); err != nil {
		t.Fatal(err)
	}
	history, err := s.Revisions(doc.Page.ID)
	if err != nil {
		t.Fatal(err)
	}
	a := newApp(notion.NewDemo([]notion.Doc{doc}), s, store.State{}, []notion.Doc{doc}, "", true)
	t.Cleanup(a.cancel)
	a.active = doc.Page.ID
	a.showDoc(a.docs[a.active])
	a.ui.SetFocus(a.editor)
	return a, doc, history[0]
}

func TestRestoreRevisionPreservesBaseAndResumesSyncAndSurvivesRestart(t *testing.T) {
	a, original, revision := revisionTestApp(t)
	id := original.Page.ID
	a.blocked[id] = true
	before := time.Now()
	if err := a.restoreRevision(id, revision); err != nil {
		t.Fatal(err)
	}
	doc := a.docs[id]
	if doc.Text != revision.Doc.Text || !doc.Dirty || !reflect.DeepEqual(doc.Base, original.Base) || !doc.Fetched.Equal(original.Fetched) || doc.Page != original.Page {
		t.Fatalf("restore imported old metadata or failed to make a draft: %+v", doc)
	}
	if a.editor.GetText() != revision.Doc.Text || a.setting || a.blocked[id] || a.changed[id].Before(before) {
		t.Fatalf("restore lost editor or retry state: changed=%v, paused=%v", a.changed, a.blocked)
	}
	history, err := a.store.Revisions(id)
	if err != nil || len(history) != 2 || history[0].Reason != "Before restore" || !reflect.DeepEqual(history[0].Doc, original) || !reflect.DeepEqual(history[1], revision) {
		t.Fatalf("original draft was not backed up intact: %+v, %v", history, err)
	}
	disk, err := a.store.LoadDoc(id)
	if err != nil || !reflect.DeepEqual(disk, *doc) {
		t.Fatalf("restored draft not durable: %+v, %v", disk, err)
	}
	reopened, err := store.Open(a.store.Dir)
	if err != nil {
		t.Fatal(err)
	}
	drafts, err := reopened.Drafts()
	if err != nil || len(drafts) != 1 {
		t.Fatalf("restored draft missing after restart: %+v, %v", drafts, err)
	}
	restarted := newApp(notion.NewDemo(drafts), reopened, store.State{}, drafts, "", true)
	defer restarted.cancel()
	restarted.autoSave(time.Now())
	if restarted.syncing() || len(restarted.changed) != 1 || restarted.docs[id].Text != revision.Doc.Text {
		t.Fatal("restored draft was not queued for safe autosync")
	}
}

func TestRestoreRevisionFollowsNormalDirtyAndIdleRules(t *testing.T) {
	for _, text := range []string{"", "Restored text", "Latest Notion base"} {
		t.Run(text, func(t *testing.T) {
			a, original, revision := revisionTestApp(t)
			revision.Doc.Text = text
			if err := a.restoreRevision(original.Page.ID, revision); err != nil {
				t.Fatal(err)
			}
			doc := a.docs[original.Page.ID]
			if doc.Text != text || doc.Dirty != (text != original.Base.Markdown) || a.blocked[original.Page.ID] {
				t.Fatalf("wrong restored edit state: %+v", doc)
			}
			a.autoSave(time.Now())
			if a.syncing() {
				t.Fatal("restore uploaded before the normal idle interval")
			}
		})
	}
}

func TestRestoreRevisionDoesNotReplaceDraftWhenUnsafeOrPersistenceFails(t *testing.T) {
	for _, failure := range []string{"syncing", "fetching", "page changed", "wrong snapshot page", "corrupt history", "draft write failed"} {
		t.Run(failure, func(t *testing.T) {
			a, original, revision := revisionTestApp(t)
			id := original.Page.ID
			a.changed[id] = original.Fetched
			originalPath := filepath.Join(a.store.Dir, "pages", id+".json")
			switch failure {
			case "syncing":
				a.savingPage = id
			case "fetching":
				a.fetching[id] = true
			case "page changed":
				a.active = "another-page"
			case "wrong snapshot page":
				revision.Doc.Page.ID = "another-page"
			case "corrupt history":
				if err := os.WriteFile(filepath.Join(a.store.Dir, "revisions", id+".json"), []byte("{broken"), 0600); err != nil {
					t.Fatal(err)
				}
			case "draft write failed":
				if err := os.Rename(originalPath, originalPath+".backup"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(originalPath, 0700); err != nil {
					t.Fatal(err)
				}
				originalPath += ".backup"
			}
			originalBytes, err := os.ReadFile(originalPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := a.restoreRevision(id, revision); err == nil {
				t.Fatal("unsafe restore succeeded")
			}
			if !reflect.DeepEqual(*a.docs[id], original) || a.editor.GetText() != original.Text || !a.changed[id].Equal(original.Fetched) || a.setting {
				t.Fatalf("failed restore changed current draft state: %+v", a.docs[id])
			}
			after, err := os.ReadFile(originalPath)
			if err != nil || string(after) != string(originalBytes) {
				t.Fatalf("failed restore changed draft on disk: %s, %v", after, err)
			}
			if failure == "draft write failed" {
				history, err := a.store.Revisions(id)
				if err != nil || len(history) != 2 || !reflect.DeepEqual(history[0].Doc, original) {
					t.Fatalf("backup missing after replacement failed: %+v, %v", history, err)
				}
			}
		})
	}
}

func TestLocalRevisionsReviewAndRestoreKeyboard(t *testing.T) {
	a, original, revision := revisionTestApp(t)
	queued := a.changed[original.Page.ID]
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(120, 40)
	a.layers.SetRect(0, 0, 120, 40)
	a.revisions()
	a.layers.Draw(screen)
	var rendered strings.Builder
	selectionFG, selectionBG, _ := selection.Decompose()
	for y := 0; y < 40; y++ {
		for x := 0; x < 120; x++ {
			r, _, style, _ := screen.GetContent(x, y)
			rendered.WriteRune(r)
			fg, bg, _ := style.Decompose()
			if !((fg == tcell.ColorDefault || fg == tcell.ColorTeal) && bg == tcell.ColorDefault) && (fg != selectionFG || bg != selectionBG) {
				t.Fatal("revision UI overrides terminal colors")
			}
		}
		rendered.WriteByte('\n')
	}
	for _, text := range []string{"Local revisions", "Fetched", "Current draft", original.Text, revision.Doc.Text} {
		if !strings.Contains(rendered.String(), text) {
			t.Errorf("revision UI missing %q", text)
		}
	}
	if !a.modal || len(a.changed) != 1 || !a.changed[original.Page.ID].Equal(queued) || !reflect.DeepEqual(*a.docs[original.Page.ID], original) {
		t.Fatal("reviewing revisions changed the document")
	}
	key := func(k tcell.Key) {
		t.Helper()
		if e := a.keys(tcell.NewEventKey(k, 0, tcell.ModNone)); e != nil {
			a.layers.InputHandler()(e, func(p tview.Primitive) { a.ui.SetFocus(p) })
		}
	}
	key(tcell.KeyEnter) // Review, not restore.
	if _, ok := a.ui.GetFocus().(*tview.TextView); !ok || a.docs[original.Page.ID].Text != original.Text {
		t.Fatal("selecting a snapshot did not safely focus its preview")
	}
	key(tcell.KeyTab) // Current draft.
	key(tcell.KeyTab) // Explicit restore button.
	button, ok := a.ui.GetFocus().(*tview.Button)
	if !ok || button.GetLabel() != "Restore as draft" {
		t.Fatalf("restore button is not keyboard reachable: %T", a.ui.GetFocus())
	}
	key(tcell.KeyEnter)
	if a.modal || a.ui.GetFocus() != a.editor || a.editor.GetText() != revision.Doc.Text {
		t.Fatal("restore did not return to the restored editor")
	}
	history, err := a.store.Revisions(original.Page.ID)
	if err != nil || len(history) != 2 || history[0].Doc.Text != original.Text {
		t.Fatalf("UI restore did not preserve the current draft: %+v, %v", history, err)
	}
}

func TestLocalRevisionsUseMacFriendlyShortcut(t *testing.T) {
	a, _, _ := revisionTestApp(t)
	if event := a.keys(tcell.NewEventKey(tcell.KeyCtrlR, 0, tcell.ModNone)); event != nil || !a.modal {
		t.Fatalf("Ctrl+R did not open local revisions: event=%v modal=%v", event, a.modal)
	}
	a.closeModal()
	if event := a.keys(tcell.NewEventKey(tcell.KeyF9, 0, tcell.ModNone)); event == nil || a.modal {
		t.Fatalf("F9 is still captured: event=%v modal=%v", event, a.modal)
	}
}

func TestEditingBlockedDraftRemainsPausedUntilExplicitRetry(t *testing.T) {
	a, original, _ := revisionTestApp(t)
	id := original.Page.ID
	a.blocked[id] = true
	a.editor.Replace(0, 1, "X")
	if !a.blocked[id] || !a.docs[id].Dirty || a.changed[id].IsZero() {
		t.Fatalf("editing did not preserve safe pause: blocked=%v dirty=%v changed=%v", a.blocked[id], a.docs[id].Dirty, a.changed[id])
	}
}
