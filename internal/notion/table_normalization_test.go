package notion

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLiveTableInsertRetryEdit(t *testing.T) {
	parent := os.Getenv("NTTY_LIVE_TABLE_PARENT")
	if parent == "" {
		t.Skip("opt-in disposable live table test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	c := NewClient()
	p, original, err := c.Create(ctx, parent, "ntty table retry regression", "Before\nAfter")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		var response any
		if err := c.api(context.Background(), "PATCH", "v1/pages/"+p.ID, map[string]bool{"in_trash": true}, false, &response); err != nil {
			t.Error(err)
		}
	}()
	table := "<table header-row=\"true\">\n\t<colgroup>\n\t\t<col>\n\t\t<col>\n\t</colgroup>\n\t<tr>\n\t\t<td>Column 1</td>\n\t\t<td>Column 2</td>\n\t</tr>\n</table>"
	wanted := strings.Replace(original.Markdown, "Before", "Before\n"+table, 1)
	for i := 0; i < 3; i++ {
		if _, err = c.Save(ctx, p.ID, original.Markdown, wanted); err != nil {
			t.Fatal(err)
		}
	}
	remote, err := c.Read(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(remote.Markdown, "<table") != 1 {
		t.Fatal("table duplicated")
	}
	edited := strings.Replace(remote.Markdown, "Column 1", "Edited cell café", 1)
	if _, err = c.Save(ctx, p.ID, remote.Markdown, edited); err != nil {
		t.Fatal(err)
	}
	read, err := c.Read(ctx, p.ID)
	if err != nil || strings.Count(read.Markdown, "<table") != 1 || !strings.Contains(read.Markdown, "Edited cell café") {
		t.Fatalf("edit not saved: %v", err)
	}
	if _, err = c.Save(ctx, p.ID, read.Markdown, original.Markdown); err != nil {
		t.Fatalf("delete table: %v", err)
	}
	read, err = c.Read(ctx, p.ID)
	if err != nil || strings.Contains(read.Markdown, "<table") {
		t.Fatalf("table deletion did not reach Notion: %v", err)
	}
}

func TestTableSaveRetryDoesNotDuplicateAndRebasesTyping(t *testing.T) {
	table := "<table header-row=\"true\">\n\t<colgroup>\n\t\t<col>\n\t\t<col>\n\t</colgroup>\n\t<tr>\n\t\t<td>Column 1</td>\n\t\t<td>Column 2</td>\n\t</tr>\n</table>"
	base := "Before\nAfter"
	wanted := "Before\n" + table + "\nAfter"
	remote := base
	writes := 0
	c := NewClient()
	c.interval = 0
	c.run = func(_ context.Context, args []string, _ []byte) ([]byte, error) {
		if args[3] == "GET" {
			return json.Marshal(Content{Object: "page_markdown", Markdown: remote})
		}
		writes++
		remote = canonicalTables(wanted)
		return nil, fmt.Errorf("response lost")
	}
	_, err := c.Save(context.Background(), "p", base, wanted)
	if err == nil {
		t.Fatal("expected lost response")
	}
	for i := 0; i < 3; i++ {
		saved, err := c.Save(context.Background(), "p", base, wanted)
		if err != nil || saved.Markdown != remote || writes != 1 {
			t.Fatalf("retry duplicated table: writes=%d err=%v", writes, err)
		}
	}
	latest := strings.Replace(wanted, "Column 1", "Edited during save", 1)
	rebased, ok, err := RebaseDraft(wanted, latest, remote)
	if err != nil || !ok || strings.Count(rebased, "<table") != 1 || !strings.Contains(rebased, "Edited during save") {
		t.Fatalf("typing lost or duplicated: %q %v", rebased, err)
	}
	if sameSavedText(wanted, strings.Replace(remote, "Column 1", "Different", 1)) {
		t.Fatal("cell content difference ignored")
	}
	if sameSavedText("```\n"+table+"\n```", "```\n"+canonicalTables(table)+"\n```") {
		t.Fatal("code contents normalized")
	}
}
