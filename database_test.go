package main

import (
	"ntty/internal/notion"
	"testing"
)

func TestDatabaseMatchesDisplayValues(t *testing.T) {
	page := notion.Page{Title: "Ship release", PropertyData: `[{"name":"Status","type":"status","text":"In progress"},{"name":"Owner","type":"rich_text","text":"Alice"}]`}
	for query, want := range map[string]bool{"": true, "ship": true, "alice": true, "Status: progress": true, "status: done": false, "Owner: Alice": true, "missing: Alice": false, "rich_text": false} {
		if got := databaseMatches(page, query); got != want {
			t.Errorf("%q: %v, want %v", query, got, want)
		}
	}
}

func TestDatabasePropertyCacheInvalidatesChangedData(t *testing.T) {
	view := &databaseView{}
	page := notion.Page{ID: "row", PropertyData: `[{"name":"Status","text":"Old"}]`}
	first := view.propertyValues(page)
	first[0].Text = "cached"
	if got := view.propertyValues(page)[0].Text; got != "cached" {
		t.Fatalf("cache miss: %q", got)
	}
	page.PropertyData = `[{"name":"Status","text":"New"}]`
	if got := view.propertyValues(page)[0].Text; got != "New" {
		t.Fatalf("stale property: %q", got)
	}
}
