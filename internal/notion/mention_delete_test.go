package notion

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestMentionDeletionPlanKeepsEmbeddedObjects(t *testing.T) {
	for _, mention := range []string{typedMention, `<mention-user url="user://12345678-1234-1234-1234-123456789abc"/>`, `<mention-date start="2026-09-21"/>`} {
		object := `<database url="https://www.notion.so/db">Tasks</database>`
		before := "Related to " + mention + " as well.\n" + object
		after := "Related to  as well.\n" + object
		if !PreservesProtectedObjects(before, after) {
			t.Fatal("mention still protected")
		}
		updates, err := planContentUpdates(before, after)
		if err != nil {
			t.Fatal(err)
		}
		applyExactUpdates(t, before, after, updates)
		if PreservesProtectedObjects(before, after[:strings.Index(after, "<database")]) {
			t.Fatal("database protection weakened")
		}
	}
}

func TestLiveMentionDeletion(t *testing.T) {
	parent := os.Getenv("NTTY_MENTION_DELETE_PARENT")
	if parent == "" {
		t.Skip("opt-in disposable live mention deletion")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	c := NewClient()
	mention := strings.Replace(typedMention, "12345678-1234-4234-8234-123456789abc", strings.TrimPrefix(parent, "page:"), 1)
	p, content, err := c.Create(ctx, parent, "ntty mention delete regression", "Before "+mention+" after\nDate <mention-date start=\"2026-09-21\"/>")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := c.SetTrash(context.Background(), p.ID, true); err != nil {
			t.Error(err)
		}
	}()
	want := "Before  after\nDate removed"
	if _, err := c.Save(ctx, p.ID, content.Markdown, want); err != nil {
		t.Fatal(err)
	}
	remote, err := c.Read(ctx, p.ID)
	if err != nil || strings.Contains(remote.Markdown, "<mention-") {
		t.Fatalf("mention deletion failed: %v %s", err, remote.Markdown)
	}
}
