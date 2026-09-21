package main

import (
	"context"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"ntty/internal/notion"
	"ntty/internal/store"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLiveMentionAndNewlinesConverge(t *testing.T) {
	parent := os.Getenv("NTTY_MENTION_STRESS_PARENT")
	if parent == "" {
		t.Skip("opt-in disposable live editor test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	c := notion.NewClient()
	page, content, err := c.Create(ctx, parent, "ntty mention newline regression", "Introduction\n<empty-block/>")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := c.SetTrash(context.Background(), page.ID, true); err != nil {
			t.Error(err)
		}
	}()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := newApp(c, s, store.State{}, nil, "", false)
	doc := notion.Doc{Page: page, Base: content, Text: content.Markdown, Fetched: time.Now()}
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	screen.SetSize(110, 32)
	a.ui.SetScreen(screen)
	done := make(chan error, 1)
	go func() { done <- a.ui.Run() }()
	defer func() {
		a.ui.QueueUpdate(func() { a.cancel(); a.ui.Stop() })
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	a.ui.QueueUpdateDraw(func() {
		a.docs[page.ID] = &doc
		a.active = page.ID
		a.showDoc(&doc)
		end := len(a.editor.visible)
		a.editor.Replace(end, end, "Related to ")
		if !a.editor.InsertAtom(`<mention-page url="https://www.notion.so/` + page.ID + `">Release notes (A&amp;B)</mention-page>`) {
			t.Error("mention insertion failed")
		}
		at := a.editor.head
		a.editor.Replace(at, at, " as well.")
	})
	for cycle := 0; cycle < 5; cycle++ {
		a.ui.QueueUpdateDraw(func() {
			a.autoSave(time.Now().Add(4 * time.Second))
			for i := 0; i < 3; i++ {
				a.editor.Select(len(a.editor.visible), len(a.editor.visible))
				a.editor.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
			}
		})
		deadline := time.Now().Add(25 * time.Second)
		saved := false
		for time.Now().Before(deadline) {
			blocked := false
			status := ""
			a.ui.QueueUpdateDraw(func() {
				a.autoSave(time.Now().Add(4 * time.Second))
				saved = !a.syncing() && !a.needsSync(page.ID)
				blocked = a.blocked[page.ID]
				status = a.status.GetText(true)
			})
			if blocked {
				t.Fatalf("cycle %d blocked: %s", cycle, status)
			}
			if saved {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if !saved {
			t.Fatal("autosave did not settle")
		}
		remote, err := c.Read(ctx, page.ID)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(remote.Markdown, "Related to") != 1 {
			t.Fatalf("cycle %d duplicated paragraph: %s", cycle, remote.Markdown)
		}
		disk, err := s.LoadDoc(page.ID)
		if err != nil || disk.Dirty || disk.Pending != nil || disk.Text != remote.Markdown {
			t.Fatalf("cycle %d failed to persist convergence: %v", cycle, err)
		}
		t.Logf("cycle %d: new lines during save, exactly one mention paragraph, clean persisted draft", cycle+1)
	}
}
