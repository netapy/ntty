package notion

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

const typedMention = `<mention-page url="https://www.notion.so/12345678-1234-4234-8234-123456789abc">Release notes (A&amp;B)</mention-page>`

func TestMentionRetryDoesNotAppendAgain(t *testing.T) {
	base := "Introduction\n<empty-block/>"
	want := base + "\nRelated to " + typedMention + " as well.\n<empty-block/>\n<empty-block/>"
	for _, lost := range []bool{false, true} {
		remote := base
		writes := 0
		c := NewClient()
		c.interval = 0
		c.run = func(_ context.Context, args []string, _ []byte) ([]byte, error) {
			if args[3] != "GET" {
				writes++
				remote = canonicalPageMentions(want)
				if lost {
					return nil, errors.New("response lost")
				}
			}
			return json.Marshal(Content{Object: "page_markdown", Markdown: remote})
		}
		_, err := c.Save(context.Background(), "p", base, want)
		if !lost && err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 5; i++ {
			if _, err := c.ReconcileSave(context.Background(), "p", base, want); err != nil {
				t.Fatal(err)
			}
		}
		if writes != 1 {
			t.Fatalf("duplicate writes: %d", writes)
		}
		latest := want + "\nNew line\n<empty-block/>"
		rebased, ok, err := RebaseDraft(want, latest, remote)
		if err != nil || !ok || strings.Count(rebased, "Related to") != 1 || !strings.Contains(rebased, "New line") {
			t.Fatalf("in-flight Enter rebase: %s %v", rebased, err)
		}
	}
	if sameSavedText("```\n"+typedMention+"\n```", "```\n"+canonicalPageMentions(typedMention)+"\n```") {
		t.Fatal("changed a code example")
	}
}

func TestUnknownCanonicalResponseNeverRewritesOnRetry(t *testing.T) {
	c := NewClient()
	c.interval = 0
	writes := 0
	c.run = func(_ context.Context, args []string, _ []byte) ([]byte, error) {
		if args[3] != "GET" {
			writes++
		}
		return json.Marshal(Content{Object: "page_markdown", Markdown: "Heading\nUnexpected server spelling\n<empty-block/>"})
	}
	for i := 0; i < 5; i++ {
		_, err := c.ReconcileSave(context.Background(), "p", "Heading", "Heading\nMy inserted block\n<empty-block/>")
		if err == nil {
			t.Fatal("unproven write accepted")
		}
	}
	if writes != 0 {
		t.Fatalf("uncertainty produced %d writes", writes)
	}
}

func TestCapturedNeoSaveConfirmsWithoutWriting(t *testing.T) {
	path := os.Getenv("NTTY_NEO_CAPTURE")
	if path == "" {
		t.Skip("captured Neo draft")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc Doc
	if err = json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if !IsRepeatedInsertionAttempt(doc.Base, doc.Pending) {
		t.Fatal("duplicate insertion checkpoint not recognized")
	}
	c := NewClient()
	c.interval = 0
	c.run = func(_ context.Context, args []string, _ []byte) ([]byte, error) {
		if args[3] != "GET" {
			t.Fatal("captured pending write duplicated again")
		}
		return json.Marshal(Content{Object: "page_markdown", Markdown: canonicalPageMentions(doc.Pending.Text)})
	}
	if _, err = c.ReconcileSave(context.Background(), doc.Page.ID, doc.Pending.Base.Markdown, doc.Pending.Text); err != nil {
		t.Fatal(err)
	}
}
