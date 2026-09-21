package notion

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestCapturedPageSavePreflight(t *testing.T) {
	if os.Getenv("NTTY_CAPTURED_DOC") == "" {
		t.Skip("captured page diagnostic")
	}
	data, err := os.ReadFile(os.Getenv("NTTY_CAPTURED_DOC"))
	if err != nil {
		t.Fatal(err)
	}
	var doc Doc
	if err = json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	remote, err := os.ReadFile(os.Getenv("NTTY_CAPTURED_REMOTE"))
	if err != nil {
		t.Fatal(err)
	}
	c := NewClient()
	c.interval = 0
	c.run = func(_ context.Context, args []string, _ []byte) ([]byte, error) {
		if args[3] != "GET" {
			t.Fatal("unexpected write: draft should already be present")
		}
		return remote, nil
	}
	_, err = c.Save(context.Background(), doc.Page.ID, doc.Base.Markdown, doc.Text)
	if err != nil {
		t.Fatal(err)
	}
	var content Content
	if err := json.Unmarshal(remote, &content); err != nil {
		t.Fatal(err)
	}
	lines := strings.SplitAfter(content.Markdown, "\n")
	for start := 0; start < 12; start++ {
		for count := 1; count <= 3; count++ {
			if start+count > 13 {
				continue
			} // The next line is a protected database.
			text := strings.Join(lines[:start], "") + strings.Join(lines[start+count:], "")
			_, err := planContentUpdates(content.Markdown, text)
			if err != nil {
				t.Errorf("delete source lines %d:%d: %v", start, start+count, err)
			}
		}
	}
}

func TestReconcileOverlappingDeletionsAndWhitespaceNormalization(t *testing.T) {
	base := "Header\n\t- [ ] Child task ? \n<empty-block/>\n- scratch one\n- scratch two \n" + strings.Repeat("<empty-block/>\n", 7) + "---\nFooter"
	local := strings.Replace(base, "<empty-block/>\n- scratch one\n- scratch two \n<empty-block/>\n<empty-block/>\n", "", 1)
	remote := strings.Replace(local, "Child task ? ", "Child task ?", 1)
	remote = strings.Replace(remote, "<empty-block/>\n", "", 1)
	got, ok, err := mergeNonOverlapping(base, local, remote)
	if err != nil || !ok || got != remote {
		t.Fatalf("already-applied deletions did not reconcile: ok=%v err=%v\ngot=%q\nwant=%q", ok, err, got, remote)
	}
}
