package notion

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestAPIPagePreservesEmojiIconAndParent(t *testing.T) {
	var raw apiPage
	if err := json.Unmarshal([]byte(`{"object":"page","id":"child","url":"https://notion.so/child","icon":{"type":"emoji","emoji":"📌"},"parent":{"type":"page_id","page_id":"parent"},"properties":{"Name":{"id":"title","type":"title","title":[{"plain_text":"Pinned note"}]}}}`), &raw); err != nil {
		t.Fatal(err)
	}
	page := raw.page()
	if page.Title != "Pinned note" || page.Icon != "📌" || page.ParentKind != "page_id" || page.ParentID != "parent" {
		t.Fatalf("page metadata: %+v", page)
	}
}

func TestDatabaseReferencesResolveDataSources(t *testing.T) {
	c := NewClient()
	c.run = func(_ context.Context, args []string, body []byte) ([]byte, error) {
		if strings.Join(args, " ") != "api v1/databases/db-id -X GET" || body != nil {
			t.Fatalf("unexpected database request: %v %s", args, body)
		}
		return []byte(`{"object":"database","data_sources":[{"id":"source-a","name":"Tasks"},{"id":"source-b","name":"Projects"}]}`), nil
	}
	pages, err := c.DataSources(context.Background(), "db-id")
	if err != nil || len(pages) != 2 || pages[0].ID != "source-a" || pages[0].Kind != "data_source" {
		t.Fatalf("sources: %+v %v", pages, err)
	}
}
