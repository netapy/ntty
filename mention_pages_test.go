package main

import (
	"strings"
	"testing"

	"ntty/internal/notion"
)

func TestMentionMenuInsertsRealPageReference(t *testing.T) {
	base := notion.Content{Object: "page_markdown", Markdown: "Hello "}
	a := syncTestApp(t, notion.Doc{Page: notion.Page{ID: "p", Kind: "page"}, Base: base, Text: base.Markdown})
	a.state.Pages = []notion.Page{{ID: "12345678-1234-1234-1234-123456789abc", Title: "Launch plan", Kind: "page"}}
	a.editor.Select(len(a.editor.visible), len(a.editor.visible))
	a.openMentionMenu()
	a.mention.input.SetText("Launch")
	if len(a.mention.items) != 1 || !strings.Contains(a.mention.items[0].label, "Launch plan") {
		t.Fatalf("page missing from mention results: %+v", a.mention.items)
	}
	a.mention.items[0].run()
	if !strings.Contains(a.editor.GetText(), `<mention-page url="https://www.notion.so/12345678-1234-1234-1234-123456789abc">Launch plan</mention-page>`) {
		t.Fatalf("not a genuine page mention: %q", a.editor.GetText())
	}
}
