package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"ntty/internal/notion"
	"ntty/internal/store"
)

// Runs the real editor, scheduler, completion/rebase and draft writer against
// an explicitly selected live page, with isolated local storage. Only uniquely
// tagged test paragraphs are added/deleted; user content is never restored from
// an old snapshot over concurrent edits.
func TestLivePageStressConvergence(t *testing.T) {
	id := os.Getenv("NTTY_STRESS_PAGE")
	if id == "" {
		t.Skip("set NTTY_STRESS_PAGE")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	client := notion.NewClient()
	page, err := client.Page(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	original, err := client.Read(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	tag := fmt.Sprintf("ntty-stress-%d", time.Now().UnixNano())
	defer func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 45*time.Second)
		defer stop()
		current, err := client.Read(cleanupCtx, id)
		if err != nil {
			t.Error(err)
			return
		}
		lines := strings.Split(current.Markdown, "\n")
		kept := lines[:0]
		for _, line := range lines {
			if !strings.HasPrefix(line, tag) {
				kept = append(kept, line)
			}
		}
		wanted := strings.Join(kept, "\n")
		if wanted != current.Markdown {
			if _, err := client.Save(cleanupCtx, id, current.Markdown, wanted); err != nil {
				t.Errorf("cleanup %s: %v", tag, err)
			}
		}
	}()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	doc := notion.Doc{Page: page, Base: original, Text: original.Markdown, Fetched: time.Now()}
	if path := os.Getenv("NTTY_STRESS_RECOVER_DOC"); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(data, &doc); err != nil {
			t.Fatal(err)
		}
		if doc.Page.ID != id {
			t.Fatal("captured draft belongs to a different page")
		}
	}
	a := newApp(client, s, store.State{}, nil, "", false)
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
		a.docs[id] = &doc
		a.active = id
		a.showDoc(&doc)
		if a.needsSync(id) {
			a.changed[id] = time.Now()
		}
	})
	waitSaved := func() {
		t.Helper()
		deadline := time.Now().Add(35 * time.Second)
		for time.Now().Before(deadline) {
			saved, blocked, status := false, false, ""
			a.ui.QueueUpdateDraw(func() {
				a.autoSave(time.Now().Add(4 * time.Second))
				saved = !a.syncing() && !a.needsSync(id)
				blocked = a.blocked[id]
				status = a.status.GetText(true)
			})
			if blocked {
				t.Fatalf("sync stuck: %s", status)
			}
			if saved {
				disk, err := s.LoadDoc(id)
				if err != nil || disk.Dirty || disk.Pending != nil {
					t.Fatalf("not converged on disk: %v", err)
				}
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("autosave did not converge within 35 seconds")
	}
	waitSaved()
	if os.Getenv("NTTY_STRESS_RECOVER_DOC") != "" {
		t.Log("captured dirty draft recovered through app scheduler and persisted clean")
	}
	if os.Getenv("NTTY_STRESS_RECOVERY_ONLY") == "1" {
		return
	}
	for cycle := 0; cycle < 3; cycle++ {
		var text strings.Builder
		for n := 0; n < 30; n++ {
			fmt.Fprintf(&text, "%s-%d-%02d café $$\n", tag, cycle, n)
		}
		a.ui.QueueUpdateDraw(func() {
			a.editor.Replace(0, 0, text.String())
			a.autoSave(time.Now().Add(4 * time.Second))
			a.editor.Replace(0, 0, tag+"-during-save\n")
		})
		waitSaved()
		remote, err := client.Read(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(remote.Markdown, tag) != 31 {
			t.Fatalf("cycle %d lost/duplicated additions: count=%d", cycle, strings.Count(remote.Markdown, tag))
		}
		a.ui.QueueUpdateDraw(func() {
			end := 0
			for _, line := range strings.Split(a.editor.visible, "\n") {
				if !strings.HasPrefix(line, tag) {
					break
				}
				end += len(line) + 1
			}
			a.editor.Replace(0, end, "")
		})
		waitSaved()
		remote, err = client.Read(ctx, id)
		if err != nil || strings.Contains(remote.Markdown, tag) {
			t.Fatalf("cycle %d deletion did not converge: %v", cycle, err)
		}
		t.Logf("cycle %d: 31 blocks added (including typing during save), persisted, deleted, and verified remotely", cycle+1)
	}
	// Exercise the incoming direction while the editor is clean.
	remote, err := client.Read(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.Save(ctx, id, remote.Markdown, tag+"-web\n"+remote.Markdown); err != nil {
		t.Fatal(err)
	}
	a.ui.QueueUpdateDraw(func() { a.remoteChecked[id] = time.Time{}; a.autoRefresh(time.Now()) })
	deadline := time.Now().Add(20 * time.Second)
	seen := false
	for time.Now().Before(deadline) {
		a.ui.QueueUpdate(func() { seen = strings.Contains(a.editor.visible, tag+"-web") })
		if seen {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !seen {
		t.Fatal("remote edit did not reach the active editor")
	}
	a.ui.QueueUpdateDraw(func() { a.editor.Replace(0, len(tag+"-web\n"), "") })
	waitSaved()
	t.Log("remote-to-editor refresh and editor-to-remote deletion converged")
}
