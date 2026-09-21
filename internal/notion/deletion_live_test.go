package notion

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// Explicitly opt-in: exercise deletion of two empty blocks immediately before
// a divider, then restore them through the normal three-way save path.
func TestLiveEmptyBlockDeletion(t *testing.T) {
	id := os.Getenv("NTTY_DELETE_TEST_PAGE")
	if id == "" {
		t.Skip("set NTTY_DELETE_TEST_PAGE")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	c := NewClient()
	original, err := c.Read(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	needle := strings.Repeat("<empty-block/>\n", 4) + "---"
	at := strings.Index(original.Markdown, needle)
	if at < 0 {
		t.Fatal("expected four-empty-block run before divider")
	}
	wanted := original.Markdown[:at] + strings.Repeat("<empty-block/>\n", 2) + "---" + original.Markdown[at+len(needle):]
	defer func() {
		// Merge restoration relative to the deletion target, retaining independent
		// edits made in the web app while this test runs.
		restoreCtx, stop := context.WithTimeout(context.Background(), 45*time.Second)
		defer stop()
		_, err := c.Save(restoreCtx, id, wanted, original.Markdown)
		if err != nil {
			t.Errorf("restore empty blocks: %v", err)
		}
	}()
	saved, err := c.Save(ctx, id, original.Markdown, wanted)
	if err != nil {
		t.Fatalf("deleting two empty blocks: %v", err)
	}
	if !sameSavedText(saved.Markdown, wanted) {
		t.Fatal("deletion did not match target")
	}
	read, err := c.Read(ctx, id)
	if err != nil || !sameSavedText(read.Markdown, wanted) {
		t.Fatalf("read-after-delete differs: %v", err)
	}
}
