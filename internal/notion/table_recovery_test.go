package notion

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestCapturedTableRecoveryMerge(t *testing.T) {
	path := os.Getenv("NTTY_CAPTURED_DOC")
	if path == "" {
		t.Skip("captured draft")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc Doc
	if err = json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(os.Getenv("NTTY_CAPTURED_REMOTE"))
	if err != nil {
		t.Fatal(err)
	}
	var remote Content
	if err = json.Unmarshal(data, &remote); err != nil {
		t.Fatal(err)
	}
	local := RepairNewDraftTables(doc.Base.Markdown, doc.Text)
	if !IsDuplicatedTableAttempt(doc.Pending) {
		t.Fatal("captured duplicate retry not recognized")
	}
	if local == doc.Text {
		t.Fatal("malformed captured table was not repaired")
	}
	base, local := alignTransientNotionURLs(doc.Base.Markdown, local, remote.Markdown)
	_, ok, err := mergeNonOverlapping(base, local, remote.Markdown)
	if err != nil || !ok {
		for name, text := range map[string]string{"local": local, "remote": remote.Markdown} {
			edits, _ := diffLineSequence(strings.Split(base, "\n"), strings.Split(text, "\n"))
			for _, e := range edits {
				t.Logf("%s %d:%d => %q", name, e.start, e.end, e.lines)
			}
		}
		t.Fatalf("captured table recovery merge failed: %v", err)
	}
	c := NewClient()
	c.interval = 0
	c.run = func(_ context.Context, args []string, body []byte) ([]byte, error) {
		if args[3] != "GET" {
			var request struct {
				Update struct {
					Updates []contentUpdate `json:"content_updates"`
				} `json:"update_content"`
			}
			if err := json.Unmarshal(body, &request); err != nil {
				t.Fatal(err)
			}
			if len(request.Update.Updates) == 0 {
				t.Fatalf("unexpected request: %s", body)
			}
			for _, u := range request.Update.Updates {
				if strings.Count(remote.Markdown, u.OldStr) != 1 {
					t.Fatal("ambiguous update")
				}
				remote.Markdown = strings.Replace(remote.Markdown, u.OldStr, u.NewStr, 1)
			}
			remote.Markdown = canonicalTables(remote.Markdown)
		}
		return json.Marshal(remote)
	}
	if _, err = c.Save(context.Background(), doc.Page.ID, doc.Base.Markdown, local); err != nil {
		t.Fatalf("captured save preflight: %v", err)
	}
}

func TestOnlyDuplicateTableCheckpointCanBeRetired(t *testing.T) {
	table := "<table>\n<tr><td>A</td></tr>\n</table>"
	p := &PendingSave{Base: Content{Markdown: table + "\nEnd"}, Text: table + "\n" + table + "\nEnd", Draft: table + "\nEnd"}
	if !IsDuplicatedTableAttempt(p) {
		t.Fatal("duplicate retry not recognized")
	}
	p.Draft = p.Text
	if IsDuplicatedTableAttempt(p) {
		t.Fatal("intentional duplicate would be discarded")
	}
	p.Draft = table + "\nEnd"
	p.Text = strings.Replace(p.Text, "A", "Edited", 1)
	if IsDuplicatedTableAttempt(p) {
		t.Fatal("distinct table edit would be discarded")
	}
}

func TestRepairMalformedNewTableKeepsAllText(t *testing.T) {
	broken := "<table>\n<tr><td>u</td>escaped<td>Header</td></tr>\n<tr><td>Value</td><td>Split</td><td>2</td></tr>after row\n</table>"
	got := RepairNewDraftTables("", broken)
	if got == broken || strings.Count(got, "<td>") != 6 || !strings.HasSuffix(got, "</table>\nescaped\nafter row") {
		t.Fatalf("recovery: %q", got)
	}
	for _, text := range []string{"u", "Header", "Value", "Split", "2", "escaped", "after row"} {
		if !strings.Contains(got, text) {
			t.Fatalf("lost %q", text)
		}
	}
	if RepairNewDraftTables("", got) != got {
		t.Fatal("recovery not idempotent")
	}
	if RepairNewDraftTables("", broken+"\n```\ncode\n```") != got+"\n```\ncode\n```" {
		t.Fatal("unrelated code prevented recovery")
	}
	if RepairNewDraftTables("", "```\n"+broken+"\n```") != "```\n"+broken+"\n```" {
		t.Fatal("repaired code example")
	}
	if RepairNewDraftTables(broken, broken) != broken {
		t.Fatal("changed an existing remote table")
	}
	if _, err := inspectMarkdown(got); err != nil {
		t.Fatal(err)
	}
}
