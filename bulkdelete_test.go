package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"ntty/internal/notion"
)

func TestDeleteSectionBeforeDatabaseKeepsObject(t *testing.T) {
	const object = `<database url="https://www.notion.so/db" inline="false">Tasks</database>`
	source := "Header\n- [ ] First\n\t- [ ] Child\n" + strings.Repeat("<empty-block/>\n", 20) + object + "\nAfter"
	r := newRichEditor()
	r.SetText(source, false)
	end := strings.Index(r.visible, "▦ Tasks")
	r.Replace(0, end, "")
	if r.GetText() != object+"\nAfter" {
		t.Fatalf("bulk deletion rejected or damaged object: %q", r.GetText())
	}
	r.history(true)
	if r.GetText() != source {
		t.Fatal("undo did not restore the full section")
	}
}

// Opt-in real-page regression. Only the ordinary section preceding the first
// database is deleted; all embeds remain exact. Restore through a three-way
// save, preserving independent web edits. Keep a disk backup before any write.
func TestLiveBulkDeleteBeforeDatabase(t *testing.T) {
	id := os.Getenv("NTTY_BULK_DELETE_PAGE")
	if id == "" {
		t.Skip("set NTTY_BULK_DELETE_PAGE")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	c := notion.NewClient()
	original, err := c.Read(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	backup, err := os.CreateTemp("", "ntty-bulk-delete-backup-*.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.NewEncoder(backup).Encode(original); err != nil {
		backup.Close()
		t.Fatal(err)
	}
	if err = backup.Close(); err != nil {
		t.Fatal(err)
	}
	t.Logf("backup: %s", backup.Name())
	r := newRichEditor()
	r.SetText(original.Markdown, false)
	end := strings.Index(r.visible, "▦ Tasks")
	if end < 0 {
		t.Fatal("Tasks database not found")
	}
	r.Replace(0, end, "")
	wanted := r.GetText()
	if wanted == original.Markdown || !strings.HasPrefix(wanted, "<database ") {
		t.Fatal("editor bulk deletion failed")
	}
	t.Logf("deleting %d visible lines before database", strings.Count(richDisplay(parseRich(original.Markdown, nil))[:end], "\n"))
	defer func() {
		restoreCtx, stop := context.WithTimeout(context.Background(), 45*time.Second)
		defer stop()
		restored, err := c.Save(restoreCtx, id, wanted, original.Markdown)
		if err != nil {
			t.Errorf("restore failed; backup retained at %s: %v", backup.Name(), err)
			return
		}
		read, err := c.Read(restoreCtx, id)
		if err != nil {
			t.Error(err)
			return
		}
		if !strings.HasPrefix(read.Markdown, strings.SplitN(original.Markdown, "<database ", 2)[0]) {
			t.Error("restored section missing")
		}
		if restored.Markdown == "" {
			t.Error("restore returned no content")
		}
	}()
	saved, err := c.Save(ctx, id, original.Markdown, wanted)
	if err != nil {
		t.Fatalf("bulk deletion save: %v", err)
	}
	read, err := c.Read(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(saved.Markdown, "<database ") || !strings.HasPrefix(read.Markdown, "<database ") {
		t.Fatal("bulk deletion did not reach Notion")
	}
}
