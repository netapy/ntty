package notion

import "testing"

func TestLiteralDollarsDoNotBlockWholePageSync(t *testing.T) {
	for _, text := range []string{"hello $$", "cost $$$", "price $5"} {
		if _, err := inspectMarkdown(text); err != nil {
			t.Fatalf("literal prose rejected: %q: %v", text, err)
		}
	}
	if _, err := inspectMarkdown("$$\nunclosed math"); err == nil {
		t.Fatal("unterminated equation block accepted")
	}
	if !sameNotionText("hello $$\n- parent\n\t\t- child", "hello $``$\n- parent\n\t- child") {
		t.Fatal("Notion canonical dollars/indentation caused a false mismatch")
	}
	if sameNotionText("`\\$`", "`$`") {
		t.Fatal("changed code content was treated as formatting normalization")
	}
	if sameNotionText("```\n\\$\n```", "```\n$\n```") {
		t.Fatal("changed fenced code was treated as normalization")
	}
}

func TestBlankBlockAlongsideConcurrentLocalInsertion(t *testing.T) {
	base := "Header\n<empty-block/>\n<empty-block/>\nFooter"
	local := "Header\n<empty-block/>\nhello $$\n<empty-block/>\n<empty-block/>\nFooter"
	remote := "Header\n<empty-block/>\n<empty-block/>\n<empty-block/>\nFooter"
	got, ok, err := mergeNonOverlapping(base, local, remote)
	if err != nil || !ok {
		t.Fatalf("concurrent additions paused sync: %v", err)
	}
	if _, err := planContentUpdates(remote, got); err != nil {
		t.Fatalf("merged additions cannot be saved: %v", err)
	}
}
