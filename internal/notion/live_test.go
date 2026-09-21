package notion

import (
	"context"
	"html"
	"os"
	"strings"
	"testing"
	"time"
)

// TestLiveSyncRoundTrip is opt-in because it writes and restores a dedicated
// disposable Notion page through the real ntn CLI. The page must contain the
// three anchors below. Never point this at a page with user content.
func TestLiveSyncRoundTrip(t *testing.T) {
	id := os.Getenv("NTTY_LIVE_PAGE_ID")
	if id == "" {
		t.Skip("set NTTY_LIVE_PAGE_ID to a disposable ntty test page")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	c := NewClient()
	original, err := c.Read(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	changed := original.Markdown
	for _, anchor := range []string{"First block", "After one blank", "After two blanks"} {
		if strings.Count(changed, anchor) != 1 {
			t.Fatalf("disposable page must contain one %q anchor", anchor)
		}
		changed = strings.Replace(changed, anchor, anchor+" — live", 1)
	}
	restored := false
	defer func() {
		if restored {
			return
		}
		current, readErr := c.Read(context.Background(), id)
		if readErr == nil && current.Markdown != original.Markdown {
			if _, restoreErr := c.Save(context.Background(), id, current.Markdown, original.Markdown); restoreErr != nil {
				t.Errorf("emergency restore: %v", restoreErr)
			}
		}
	}()
	saved, err := c.Save(ctx, id, original.Markdown, changed)
	if err != nil {
		t.Fatal(err)
	}
	readBack, err := c.Read(ctx, id)
	if err != nil || readBack.Markdown != saved.Markdown {
		t.Fatalf("read-after-write: err=%v equal=%v", err, readBack.Markdown == saved.Markdown)
	}
	if _, err := c.Save(ctx, id, saved.Markdown, original.Markdown); err != nil {
		t.Fatal(err)
	}
	final, err := c.Read(ctx, id)
	if err != nil || final.Markdown != original.Markdown {
		t.Fatalf("restore verification: err=%v equal=%v", err, final.Markdown == original.Markdown)
	}
	restored = true
}

// TestLiveDataSourceQuery is read-only and verifies that real API row
// properties survive the ntn transport and become displayable TUI values.
func TestLiveDataSourceQuery(t *testing.T) {
	id := os.Getenv("NTTY_LIVE_DATA_SOURCE_ID")
	if id == "" {
		t.Skip("set NTTY_LIVE_DATA_SOURCE_ID to an accessible data source")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	listing, err := NewClient().Query(ctx, id, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(listing.Pages) == 0 {
		t.Fatal("data source returned no rows")
	}
	for _, page := range listing.Pages {
		if page.ID == "" || page.Title == "" || page.ParentKind != "data_source_id" || page.ParentID != id {
			t.Fatalf("invalid row: %+v", page)
		}
		if len(page.PropertyValues()) == 0 {
			t.Fatalf("row %s has no decoded properties", page.ID)
		}
	}
}

// TestLiveMentionRoundTrip writes only to the same dedicated disposable page
// as TestLiveSyncRoundTrip and always restores its exact original Markdown.
func TestLiveMentionRoundTrip(t *testing.T) {
	id, personID, personName := os.Getenv("NTTY_LIVE_PAGE_ID"), os.Getenv("NTTY_LIVE_PERSON_ID"), os.Getenv("NTTY_LIVE_PERSON_NAME")
	if id == "" || personID == "" || personName == "" {
		t.Skip("set NTTY_LIVE_PAGE_ID and NTTY_LIVE_PERSON_ID/NAME for the disposable page")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	c := NewClient()
	original, err := c.Read(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	date := time.Now().Format("2006-01-02")
	line := `ntty live mention: <mention-user url="{{user://` + html.EscapeString(personID) + `}}">` + html.EscapeString(personName) + `</mention-user> <mention-date start="` + date + `"/>`
	changed := strings.TrimRight(original.Markdown, "\n") + "\n\n" + line + "\n"
	restored := false
	defer func() {
		if restored {
			return
		}
		current, readErr := c.Read(context.Background(), id)
		if readErr == nil && current.Markdown != original.Markdown {
			if _, restoreErr := c.Save(context.Background(), id, current.Markdown, original.Markdown); restoreErr != nil {
				t.Errorf("emergency mention restore: %v", restoreErr)
			}
		}
	}()
	saved, err := c.Save(ctx, id, original.Markdown, changed)
	if err != nil {
		t.Fatalf("save: %v\nresponse: %q\nrequested: %q", err, saved.Markdown, changed)
	}
	if !strings.Contains(saved.Markdown, `<mention-user url="user://`+personID+`"/>`) || !strings.Contains(saved.Markdown, `<mention-date start="`+date+`"`) {
		t.Fatalf("Notion did not preserve mentions: %q", saved.Markdown)
	}
	if _, err := c.Save(ctx, id, saved.Markdown, original.Markdown); err != nil {
		t.Fatal(err)
	}
	final, err := c.Read(ctx, id)
	if err != nil || final.Markdown != original.Markdown {
		t.Fatalf("mention restore verification: err=%v equal=%v", err, final.Markdown == original.Markdown)
	}
	restored = true
}

// TestLiveMultiblockInsertionRoundTrip guards against narrowing a block-level
// replacement into inline formatting, which can make Notion import only a
// prefix of the requested blocks.
func TestLiveMultiblockInsertionRoundTrip(t *testing.T) {
	id := os.Getenv("NTTY_LIVE_PAGE_ID")
	if id == "" {
		t.Skip("set NTTY_LIVE_PAGE_ID to the disposable ntty test page")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	c := NewClient()
	original, err := c.Read(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	const anchor = "First block"
	if strings.Count(original.Markdown, anchor) != 1 {
		t.Fatalf("disposable page must contain one %q anchor", anchor)
	}
	replacement := "**First block**\n<empty-block/>\n**Semaine**\n- [ ] Réponse\n- [ ] Alerting\n- [ ] "
	changed := strings.Replace(original.Markdown, anchor, replacement, 1)
	restored := false
	defer func() {
		if restored {
			return
		}
		current, readErr := c.Read(context.Background(), id)
		if readErr == nil && current.Markdown != original.Markdown {
			if _, restoreErr := c.Save(context.Background(), id, current.Markdown, original.Markdown); restoreErr != nil {
				t.Errorf("emergency multiblock restore: %v", restoreErr)
			}
		}
	}()
	saved, err := c.Save(ctx, id, original.Markdown, changed)
	if err != nil {
		t.Fatalf("multiblock save: %v", err)
	}
	for _, expected := range []string{"**Semaine**", "- [ ] Réponse", "- [ ] Alerting", "- [ ]"} {
		if !strings.Contains(saved.Markdown, expected) {
			t.Fatalf("Notion dropped %q from multiblock insertion", expected)
		}
	}
	if _, err := c.Save(ctx, id, saved.Markdown, original.Markdown); err != nil {
		t.Fatal(err)
	}
	final, err := c.Read(ctx, id)
	if err != nil || final.Markdown != original.Markdown {
		t.Fatalf("multiblock restore verification: err=%v equal=%v", err, final.Markdown == original.Markdown)
	}
	restored = true
}

// TestLiveTableRoundTrip verifies the same enhanced-Markdown table emitted by
// ntty's /table command and restores the disposable page exactly afterward.
func TestLiveTableRoundTrip(t *testing.T) {
	id := os.Getenv("NTTY_LIVE_PAGE_ID")
	if id == "" {
		t.Skip("set NTTY_LIVE_PAGE_ID to the disposable ntty test page")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	c := NewClient()
	original, err := c.Read(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	const anchor = "First block"
	if strings.Count(original.Markdown, anchor) != 1 {
		t.Fatalf("disposable page must contain one %q anchor", anchor)
	}
	const table = `<table fit-page-width="true" header-row="true">
	<colgroup>
		<col>
		<col>
	</colgroup>
	<tr>
		<td>Name</td>
		<td>Owner</td>
	</tr>
	<tr>
		<td>Roadmap</td>
		<td>Ada</td>
	</tr>
</table>`
	changed := strings.Replace(original.Markdown, anchor, anchor+"\n"+table, 1)
	restored := false
	defer func() {
		if restored {
			return
		}
		current, readErr := c.Read(context.Background(), id)
		if readErr == nil && current.Markdown != original.Markdown {
			if _, restoreErr := c.Save(context.Background(), id, current.Markdown, original.Markdown); restoreErr != nil {
				t.Errorf("emergency table restore: %v", restoreErr)
			}
		}
	}()
	saved, err := c.Save(ctx, id, original.Markdown, changed)
	if err != nil {
		t.Fatalf("table save: %v", err)
	}
	for _, expected := range []string{"<table", "<td>Name</td>", "<td>Roadmap</td>"} {
		if !strings.Contains(saved.Markdown, expected) {
			t.Fatalf("Notion dropped table content %q: %q", expected, saved.Markdown)
		}
	}
	if _, err := c.Save(ctx, id, saved.Markdown, original.Markdown); err != nil {
		t.Fatal(err)
	}
	final, err := c.Read(ctx, id)
	if err != nil || final.Markdown != original.Markdown {
		t.Fatalf("table restore verification: err=%v equal=%v", err, final.Markdown == original.Markdown)
	}
	restored = true
}
