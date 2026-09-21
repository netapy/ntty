package notion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

// Independent, intentionally literal model of the documented exact-match API.
// Count overlapping matches too: strings.Count("aaa", "aa") returns only 1.
func applyExactUpdates(t testing.TB, base, want string, updates []contentUpdate) {
	t.Helper()
	for _, u := range updates {
		if u.OldStr == "" || u.ReplaceAllMatches || !utf8.ValidString(u.OldStr) || !utf8.ValidString(u.NewStr) {
			t.Fatalf("invalid exact update: %#v", u)
		}
		found := -1
		for i := 0; i+len(u.OldStr) <= len(base); i++ {
			if base[i:i+len(u.OldStr)] == u.OldStr {
				if found >= 0 {
					t.Fatalf("ambiguous target %q in %q", u.OldStr, base)
				}
				found = i
			}
		}
		if found < 0 {
			t.Fatalf("missing target %q in %q", u.OldStr, base)
		}
		base = base[:found] + u.NewStr + base[found+len(u.OldStr):]
	}
	if base != want {
		t.Fatalf("replacement lost or added bytes:\n got %q\nwant %q", base, want)
	}
}

func TestSafeUpdatePlans(t *testing.T) {
	cases := []struct{ name, base, text string }{
		{"one word", "Before\nOrdinary prose here.\nAfter", "Before\nClear prose here.\nAfter"},
		{"insertion", "Keep this paragraph.", "Keep this lovely paragraph."},
		{"append", "First\nLast\n", "First\nLast\nAdded\n"},
		{"prepend", "First\nLast", "Added\nFirst\nLast"},
		{"delete", "First\nMiddle\nLast", "First\nLast"},
		{"clear ordinary page", "Ordinary content", ""},
		{"overlapping matches", "aaaa", "abaa"},
		{"shared unicode prefix", "Préfixe café suffixe", "Préfixe cafê suffixe"},
		{"shared unicode suffix", "Price: €10", "Price: ₭10"},
		{"unicode insertion", "🧑🏽‍💻 résumé 🇫🇷", "🧑🏽‍💻 **résumé** 🇫🇷"},
		{"crlf", "Head\r\nBefore\r\nFoot", "Head\r\nAfter\r\nFoot"},
		{"formatting", "This text is plain.", "This text is **bold** and *italic*."},
		{"underline", "Please underline this", `Please <span underline="true">underline</span> this`},
		{"color change", `<span color="red">Hello</span>`, `<span color="blue">Hello</span>`},
		{"line break", "Hello world", "Hello<br>world"},
		{"new tag suffix", "Hello >", "Hello <br>"},
		{"empty block", "<empty-block/>", "First text"},
		{"literal code tags", "```html\n<unknown/>\n```\nOutside", "```html\n<example/>\n```\nOutside"},
		{"inline literal", "Example: `<unknown/>`.", "Example: `<different/>`."},
		{"inline equation", "Math: $x < y$ and prose.", "Math: $x < z$ and prose."},
		{"code contains backticks", "```text\nExample ``` inline\n<unknown/>\n```\nAfter", "```text\nExample ``` inline\n<changed/>\n```\nAfter"},
		{"escaped tag", `Example: \<unknown/\>`, `Example: \<different/\>`},
		{"callout", "<callout icon=\"💡\" color=\"blue_bg\">\n\tReadable prose\n</callout>", "<callout icon=\"💡\" color=\"blue_bg\">\n\t**Readable prose**\n</callout>"},
		{"mention", `Hello <mention-page url="{{https://notion.so/p}}"/> world`, `Hello <mention-page url="{{https://notion.so/p}}"/> **world**`},
		{"insert user mention", "Hello team", `Hello <mention-user url="{{user://abc}}">Ada</mention-user> team`},
		{"insert date mention", "Due date", `Due <mention-date start="2026-09-21"/> date`},
		{"remove editable mention", `Hello <mention-user url="{{user://abc}}">Ada</mention-user> team`, "Hello team"},
		{"discussion", `<span discussion-urls="https://notion.so/p#discussion">Discussed text</span> after`, `<span discussion-urls="https://notion.so/p#discussion">**Discussed text**</span> after`},
		{"block metadata", `Heading {color="blue" discussion-urls="thread"}`, `**Heading** {color="blue" discussion-urls="thread"}`},
		{"unknown flags not required", "Before\n<unknown url=\"id\" alt=\"bookmark\"/>\nAfter", "Updated before\n<unknown url=\"id\" alt=\"bookmark\"/>\nUpdated after"},
		{"future opaque tag", "Before\n<future-block ref=\"id\">\nData\n</future-block>\nAfter", "Updated\n<future-block ref=\"id\">\nData\n</future-block>\nAfter"},
		{"synced untouched", "Before\n<synced_block url=\"id\">\n\tShared\n</synced_block>\nAfter", "Before\n<synced_block url=\"id\">\n\tShared\n</synced_block>\nAfter edited"},
		{"nested containers", "<columns>\n\t<column>\n\t\t<callout>\n\t\t\tEdit me\n\t\t</callout>\n\t</column>\n</columns>", "<columns>\n\t<column>\n\t\t<callout>\n\t\t\t**Edit me**\n\t\t</callout>\n\t</column>\n</columns>"},
		{"media untouched", "Before\n![image](https://host/a_(b).png)\nAfter", "Before updated\n![image](https://host/a_(b).png)\nAfter"},
		{"separate distant lines", "First original\nUntouched middle\nLast original", "First revised\nUntouched middle\nLast revised"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			updates, err := planContentUpdates(tc.base, tc.text)
			if err != nil {
				t.Fatal(err)
			}
			applyExactUpdates(t, tc.base, tc.text, updates)
			if tc.name == "separate distant lines" {
				if len(updates) != 2 {
					t.Fatalf("want two local edits, got %#v", updates)
				}
				for _, u := range updates {
					if strings.Contains(u.OldStr, "Untouched") || strings.Contains(u.NewStr, "Untouched") {
						t.Fatal("rewrote an unchanged intervening block")
					}
				}
			}
		})
	}
}

func TestDeletionCanExposeProtectedObjectAtDocumentEdges(t *testing.T) {
	object := `<database url="https://www.notion.so/db">Tasks</database>`
	for _, pair := range [][2]string{
		{"Header\n" + object + "\nFooter", object + "\nFooter"},
		{"Header\n" + object + "\nFooter", "Header\n" + object},
		{"Header\n" + object + "\nFooter", object},
	} {
		if !PreservesProtectedObjects(pair[0], pair[1]) {
			t.Fatal("object boundary mistaken for object content")
		}
		updates, err := planContentUpdates(pair[0], pair[1])
		if err != nil {
			t.Fatal(err)
		}
		applyExactUpdates(t, pair[0], pair[1], updates)
		for _, update := range updates {
			if strings.Contains(update.OldStr, "<database") {
				t.Fatal("included database in rewrite")
			}
		}
	}
}

func TestMultiblockInsertionTargetsWholeSourceLine(t *testing.T) {
	base := `**Point <mention-date start="2026-09-07"/> avec Léa**` + "\n---"
	want := `**Point **<mention-date start="2026-09-07"/>** avec Léa**` + "\n<empty-block/>\n**Semaine**\n- [ ] Alerting\n---"
	updates, err := planContentUpdates(base, want)
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) != 1 || strings.TrimSuffix(updates[0].OldStr, "\n") != strings.Split(base, "\n")[0] || !strings.HasPrefix(updates[0].NewStr, "**Point **") {
		t.Fatalf("multiblock update was narrowed inside inline formatting: %#v", updates)
	}
	applyExactUpdates(t, base, want, updates)
}

func TestNearbyInterferingInsertionsCoalesceSafely(t *testing.T) {
	base := "Point\n<empty-block/>\n<empty-block/>\n- [ ] Réponse\n- [ ]\n<empty-block/>\n---"
	want := "Point\n<empty-block/>\n<empty-block/>\n**Semaine**\n- [ ] Réponse\n- [ ] Alerting\n- [ ] \n<empty-block/>\n---"
	updates, err := planContentUpdates(base, want)
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) != 1 {
		t.Fatalf("interfering nearby targets were not coalesced: %#v", updates)
	}
	applyExactUpdates(t, base, want, updates)
}

func TestSafeUpdateRejectsUnsafeChanges(t *testing.T) {
	cases := []struct{ name, base, text string }{
		{"empty race", "", "First text"},
		{"invalid base utf8", "Bad\xff", "Fixed"},
		{"invalid draft utf8", "Good", "Bad\xff"},
		{"nul", "Good", "Bad\x00"},
		{"delete unknown", "<unknown alt=\"bookmark\"/>\nText", "Text"},
		{"change unknown", "<unknown url=\"one\"/>\nText", "<unknown url=\"two\"/>\nText"},
		{"unknown inner text", "<future>Data</future>\nText", "<future>Gone</future>\nText"},
		{"synced contents", "<synced_block url=\"id\">\n\tShared\n</synced_block>", "<synced_block url=\"id\">\n\tChanged\n</synced_block>"},
		{"synced reference", "<synced_block_reference url=\"id\">Shared</synced_block_reference>", "<synced_block_reference url=\"id\">Changed</synced_block_reference>"},
		{"delete child page", "<page url=\"p\">Child</page>\nText", "Text"},
		{"delete database", "<database url=\"d\">DB</database>\nText", "Text"},
		{"delete media", "![caption](https://host/a.png)\nText", "Text"},
		{"change discussion", `<span discussion-urls="thread">Text</span>`, `<span discussion-urls="other">Text</span>`},
		{"delete discussion", `<span discussion-urls="thread">Text</span>`, "Text"},
		{"empty discussion", `<span discussion-urls="thread">Text</span>`, `<span discussion-urls="thread"></span>`},
		{"delete metadata", `Text {discussion-urls="thread"}`, "Text"},
		{"delete callout wrapper", "<callout icon=\"💡\">\n\tText\n</callout>", "Text"},
		{"join object into line", "Intro\n<unknown/>\nAfter", "Intro<unknown/>\nAfter"},
		{"escape object", "<unknown/>\nAfter", "\\<unknown/>\nAfter"},
		{"hide in code", "<unknown/>\nAfter", "`<unknown/>`\nAfter"},
		{"unterminated tag", "<unknown url=\"p\"\nText", "<unknown url=\"p\"\nChanged"},
		{"unterminated quote", "Text", "Text <mention-page url=\"p/>"},
		{"mismatched tags", "<callout>Text</details>", "<callout>Changed</details>"},
		{"unclosed container", "<callout>Text", "<callout>Changed"},
		{"unclosed comment", "Text <!-- metadata", "Changed <!-- metadata"},
		{"unclosed code", "Text", "```\n<unknown/>"},
		{"fake tilde fence", "Text ~~~\n<unknown/>\n~~~", "Text ~~~\nRemoved\n~~~"},
		{"fake inline code fence", "Text ```\n<unknown/>\n```", "Text ```\nRemoved\n```"},
		{"fake autolink hides object", "<https://host/<unknown/>>", "Removed"},
		{"malformed attributes", "Text", `{discussion-urls="thread}`},
		{"malformed image", "Text", "![caption](https://example.com"},
		{"same protected gaps ambiguous", `<mention-unknown url="a"/>same<mention-unknown url="b"/>same<mention-unknown url="c"/>`, `<mention-unknown url="a"/>different<mention-unknown url="b"/>same<mention-unknown url="c"/>`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			updates, err := planContentUpdates(tc.base, tc.text)
			if !errors.Is(err, ErrUnsafeUpdate) || len(updates) > 0 {
				t.Fatalf("unsafe change accepted: %#v, %v", updates, err)
			}
		})
	}
}

func TestSafeUpdateExhaustiveShortEdits(t *testing.T) {
	// All before/after strings through length 3 exercise insertions, deletions,
	// overlapping targets, newlines, and UTF-8 common-byte boundaries.
	values := []string{""}
	level := []string{""}
	for length := 1; length <= 3; length++ {
		var next []string
		for _, prefix := range level {
			for _, atom := range []string{"a", "b", "é", "ê", "€", "₭", "\n"} {
				next = append(next, prefix+atom)
			}
		}
		values = append(values, next...)
		level = next
	}
	accepted, rejected := 0, 0
	for _, base := range values {
		for _, text := range values {
			updates, err := planContentUpdates(base, text)
			if err != nil {
				if !errors.Is(err, ErrUnsafeUpdate) {
					t.Fatal(err)
				}
				rejected++
				continue
			}
			accepted++
			applyExactUpdates(t, base, text, updates)
		}
	}
	if accepted < 100_000 {
		t.Fatalf("vacuous protection: only %d accepted plans", accepted)
	}
	t.Logf("verified %d exact plans; %d unsafe plans rejected", accepted, rejected)
}

func TestSafeUpdateRichPageProperties(t *testing.T) {
	rng := rand.New(rand.NewSource(20260920))
	protected := []string{
		`<unknown url="p" alt="bookmark"/>`,
		"<synced_block url=\"p\">\n\tShared\n</synced_block>",
		`<mention-page url="p"/>`,
		`<span discussion-urls="thread">Discussed</span>`,
		`<file src="https://host/file">Caption</file>`,
		`<future-object metadata="preserve">Opaque data</future-object>`,
	}
	for n := 0; n < 2_000; n++ {
		var before, after strings.Builder
		for i := 0; i < 4; i++ {
			plain := fmt.Sprintf("Section %d: café %d is worth keeping.", i, rng.Intn(1_000))
			before.WriteString(plain + "\n")
			if rng.Intn(2) == 0 {
				plain = strings.Replace(plain, "café", "**résumé**", 1)
			}
			after.WriteString(plain + "\n")
			object := protected[rng.Intn(len(protected))]
			before.WriteString(object + "\n")
			after.WriteString(object + "\n")
		}
		base, text := before.String(), after.String()
		updates, err := planContentUpdates(base, text)
		if err != nil {
			t.Fatalf("ordinary rich-page edit refused: %v\n%s\n%s", err, base, text)
		}
		applyExactUpdates(t, base, text, updates)
		for _, u := range updates {
			if strings.ContainsAny(u.OldStr, "<>") || strings.ContainsAny(u.NewStr, "<>") {
				t.Fatalf("object was included in replacement: %#v", u)
			}
		}
		// Four edits are cheap enough to check every ordering, not just the one
		// returned by the planner. The API doesn't promise batch order.
		var permute func(int)
		permute = func(at int) {
			if at == len(updates) {
				applyExactUpdates(t, base, text, updates)
				return
			}
			for i := at; i < len(updates); i++ {
				updates[at], updates[i] = updates[i], updates[at]
				permute(at + 1)
				updates[at], updates[i] = updates[i], updates[at]
			}
		}
		permute(0)
	}
}

func TestSafeUpdateEveryProtectedByteIsPreserved(t *testing.T) {
	for _, object := range []string{
		`<unknown url="p" alt="bookmark"/>`,
		`<future-object metadata="id">Unknown data</future-object>`,
		"<synced_block url=\"p\">\n\tShared text\n</synced_block>",
		`<page url="p">Child page</page>`,
		`<database url="d">Database</database>`,
		`![image caption](https://host/image_(1).png)`,
	} {
		base := "Editable before\n" + object + "\nEditable after"
		for i := range len(object) {
			for _, mutation := range []string{
				object[:i] + object[i+1:],
				object[:i] + "X" + object[i+1:],
				object[:i] + "X" + object[i:],
			} {
				if mutation == object || strings.HasSuffix(mutation, object) {
					// Inserting prose before an inline mention is safe; it has
					// not changed a byte of the protected object itself.
					continue
				}
				text := "Editable before\n" + mutation + "\nEditable after"
				if updates, err := planContentUpdates(base, text); !errors.Is(err, ErrUnsafeUpdate) || len(updates) != 0 {
					t.Fatalf("modified protected byte %d: %q -> %q: %#v, %v", i, object, mutation, updates, err)
				}
			}
		}
	}
}

func TestSafeUpdateAllowsNewStructuredBlocksWithoutTouchingExistingOnes(t *testing.T) {
	const table = `<table fit-page-width="true" header-row="true">
	<colgroup>
		<col>
		<col>
	</colgroup>
	<tr>
		<td>Name</td>
		<td>Owner</td>
	</tr>
</table>`
	base := "Before\n<empty-block/>\nAfter"
	want := "Before\n" + table + "\nAfter"
	updates, err := planContentUpdates(base, want)
	if err != nil {
		t.Fatalf("new table was rejected: %v", err)
	}
	applyExactUpdates(t, base, want, updates)

	base = "Before\n<unknown id=\"keep\"/>\nAfter"
	want = "Before\n<unknown id=\"keep\"/>\n" + table + "\nAfter"
	updates, err = planContentUpdates(base, want)
	if err != nil {
		t.Fatalf("insertion beside an existing object was rejected: %v", err)
	}
	applyExactUpdates(t, base, want, updates)
	if !PreservesProtectedObjects(base, want) {
		t.Fatal("adding a table was mistaken for deleting an existing object")
	}
}

func TestSafeUpdateBoundedPlans(t *testing.T) {
	makePage := func(n int, word string) string {
		var b strings.Builder
		for i := 0; i < n; i++ {
			fmt.Fprintf(&b, "Line %d: %s\nUntouched separator\n", i, word)
		}
		return b.String()
	}
	base, text := makePage(100, "before"), makePage(100, "after")
	updates, err := planContentUpdates(base, text)
	if err != nil || len(updates) != 100 {
		t.Fatalf("documented 100-operation batch refused: %d, %v", len(updates), err)
	}
	applyExactUpdates(t, base, text, updates)
	for _, pair := range [][2]string{
		{makePage(101, "before"), makePage(101, "after")},
		{strings.Repeat("a\n", 1_002), strings.Repeat("b\n", 1_002)},
		{strings.Repeat(strings.Repeat("a", 20_000)+"\n", 4), strings.Repeat("a", 20_000) + "\n" + "changed\n" + strings.Repeat(strings.Repeat("a", 20_000)+"\n", 2)},
	} {
		updates, err := planContentUpdates(pair[0], pair[1])
		if err != nil || len(updates) == 0 || len(updates) > 100 {
			t.Fatalf("large exact update was needlessly refused: %d, %v", len(updates), err)
		}
		applyExactUpdates(t, pair[0], pair[1], updates)
	}

	// More than 100 independently protected regions genuinely cannot fit in
	// one request without including an atomic object in a replacement target.
	var protectedBase, protectedText strings.Builder
	for i := 0; i < 101; i++ {
		fmt.Fprintf(&protectedBase, "Section %03d before\n<unknown id=\"%03d\"/>\n", i, i)
		fmt.Fprintf(&protectedText, "Section %03d after\n<unknown id=\"%03d\"/>\n", i, i)
	}
	if updates, err := planContentUpdates(protectedBase.String(), protectedText.String()); !errors.Is(err, ErrUnsafeUpdate) || len(updates) != 0 {
		t.Fatalf("more than 100 protected regions should require another save: %d, %v", len(updates), err)
	}
}

func TestSaveUsesOnlyTargetedUpdates(t *testing.T) {
	base := "Before prose\n<unknown url=\"p\" alt=\"bookmark\"/>\n<callout icon=\"💡\">\n\tInside prose\n</callout>\nAfter prose"
	text := strings.ReplaceAll(base, "prose", "**prose**")
	c := NewClient()
	c.interval = 0
	var methods []string
	c.run = func(_ context.Context, args []string, body []byte) ([]byte, error) {
		methods = append(methods, args[3])
		if args[1] != "v1/pages/p/markdown" {
			t.Fatalf("unexpected endpoint: %v", args)
		}
		if args[3] == "GET" {
			if body != nil {
				t.Fatal("read must have no body")
			}
			return json.Marshal(Content{Object: "page_markdown", ID: "p", Markdown: base})
		}
		var req struct {
			Type   string `json:"type"`
			Update struct {
				Updates []contentUpdate `json:"content_updates"`
				Delete  *bool           `json:"allow_deleting_content"`
			} `json:"update_content"`
		}
		dec := json.NewDecoder(strings.NewReader(string(body)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Type != "update_content" || req.Update.Delete == nil || *req.Update.Delete || len(req.Update.Updates) != 3 {
			t.Fatalf("unsafe body: %s", body)
		}
		for _, u := range req.Update.Updates {
			if strings.ContainsAny(u.OldStr+u.NewStr, "<>") || u.OldStr == base {
				t.Fatalf("rewrote unaffected rich content: %#v", u)
			}
		}
		applyExactUpdates(t, base, text, req.Update.Updates)
		return json.Marshal(Content{Object: "page_markdown", ID: "p", Markdown: text})
	}
	result, err := c.Save(context.Background(), "p", base, text)
	if err != nil || result.Markdown != text || !reflect.DeepEqual(methods, []string{"GET", "PATCH"}) {
		t.Fatalf("save: %+v, %v; methods %v", result, err, methods)
	}
}

func TestSaveFailClosedAndReconciliation(t *testing.T) {
	for _, tc := range []struct {
		name, base, text string
		remote           Content
		want             error
	}{
		{"empty", "", "new", Content{Object: "page_markdown"}, ErrUnsafeUpdate},
		{"conflict", "old", "mine", Content{Object: "page_markdown", Markdown: "theirs"}, ErrConflict},
		{"truncated", "old", "mine", Content{Object: "page_markdown", Markdown: "old", Truncated: true}, ErrIncomplete},
		{"unknown ids", "old", "mine", Content{Object: "page_markdown", Markdown: "old", Unknown: []string{"id"}}, ErrIncomplete},
		{"unsafe", "<unknown/>\ntext", "text", Content{Object: "page_markdown", Markdown: "<unknown/>\ntext"}, ErrUnsafeUpdate},
		{"unchanged", "same", "same", Content{Object: "page_markdown", Markdown: "same"}, nil},
		{"lost response", "old", "mine", Content{Object: "page_markdown", Markdown: "mine"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := NewClient()
			c.interval = 0
			calls := 0
			c.run = func(_ context.Context, args []string, _ []byte) ([]byte, error) {
				calls++
				if args[3] != "GET" {
					t.Fatal("unsafe or unnecessary write")
				}
				return json.Marshal(tc.remote)
			}
			_, err := c.Save(context.Background(), "p", tc.base, tc.text)
			if !errors.Is(err, tc.want) || calls != 1 {
				t.Fatalf("got %v (%d calls), want %v", err, calls, tc.want)
			}
		})
	}
}

func TestSaveDoesNotRetryAmbiguousWritesOrAcceptDifferentContent(t *testing.T) {
	for _, response := range []string{
		`{"object":"page_markdown","markdown":"dropped suffix"}`,
		`{"object":"page_markdown","markdown":"new text","truncated":true}`,
		`{"object":"page_markdown","markdown":"new text","unknown_block_ids":["id"]}`,
		`{"object":"async_task","status":"running"}`,
		`{"object":"error","status":503,"code":"service_unavailable","message":"response lost"}`,
		`{"object":"error","status":400,"code":"validation_error","message":"old_str is ambiguous"}`,
	} {
		c := NewClient()
		c.interval = 0
		calls := 0
		c.run = func(_ context.Context, args []string, _ []byte) ([]byte, error) {
			calls++
			if args[3] == "GET" {
				return []byte(`{"object":"page_markdown","markdown":"old text"}`), nil
			}
			return []byte(response), nil
		}
		_, err := c.Save(context.Background(), "p", "old text", "new text")
		if err == nil || calls != 2 {
			t.Fatalf("bad response accepted or retried: %s, %v, %d calls", response, err, calls)
		}
	}
}

func TestSaveEncodesEditedBlankBlocks(t *testing.T) {
	c := NewClient()
	c.interval = 0
	calls := 0
	c.run = func(_ context.Context, args []string, _ []byte) ([]byte, error) {
		calls++
		if args[3] == "GET" {
			return []byte(`{"object":"page_markdown","markdown":"<empty-block/>"}`), nil
		}
		return []byte(`{"object":"page_markdown","markdown":"# Heading\n<empty-block/>\nParagraph\n<empty-block/>\n- [ ] Task\n<empty-block/>"}`), nil
	}
	want := "# Heading\n\nParagraph\n\n- [ ] Task\n"
	result, err := c.Save(context.Background(), "p", "<empty-block/>", want)
	if err != nil || !result.Normalized || result.Markdown == want || calls != 2 {
		t.Fatalf("normalized save: %+v %v calls=%d", result, err, calls)
	}
	if sameNotionText("````\na\n\nb\n````", "````\na\nb\n````") {
		t.Fatal("blank code line was treated as Notion block spacing")
	}
	if sameNotionText("<empty-block/>\nParagraph", "Paragraph") {
		t.Fatal("a real empty Notion block was treated as Markdown spacing")
	}
}

func TestSaveReconcilesEarlierNormalizedWrite(t *testing.T) {
	c := NewClient()
	c.interval = 0
	calls := 0
	c.run = func(_ context.Context, args []string, _ []byte) ([]byte, error) {
		calls++
		if args[3] != "GET" {
			t.Fatal("reconciliation repeated an earlier write")
		}
		return []byte(`{"object":"page_markdown","markdown":"# Heading\nParagraph"}`), nil
	}
	result, err := c.Save(context.Background(), "p", "<empty-block/>", "# Heading\n\nParagraph\n")
	if err != nil || !result.Normalized || calls != 1 {
		t.Fatalf("normalized reconciliation: %+v %v calls=%d", result, err, calls)
	}
}

func TestSaveCancellationAfterPreflight(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := NewClient()
	c.interval = 0
	calls := 0
	c.run = func(_ context.Context, _ []string, _ []byte) ([]byte, error) {
		calls++
		cancel()
		return []byte(`{"object":"page_markdown","markdown":"old text"}`), nil
	}
	_, err := c.Save(ctx, "p", "old text", "new text")
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("write ran after cancellation: %v, %d calls", err, calls)
	}
}

func TestSaveRateLimitBackoffCanBeCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := NewClient()
	c.interval = 0
	calls := 0
	c.run = func(_ context.Context, args []string, _ []byte) ([]byte, error) {
		calls++
		if args[3] == "GET" {
			return []byte(`{"object":"page_markdown","markdown":"old text"}`), nil
		}
		cancel()
		return []byte(`{"object":"error","status":429,"code":"rate_limited","message":"Retry-After: 60"}`), nil
	}
	_, err := c.Save(ctx, "p", "old text", "new text")
	if !errors.Is(err, context.Canceled) || calls != 2 {
		t.Fatalf("backoff ignored cancellation or retried: %v, %d calls", err, calls)
	}
}

func TestEditorGuardPreservesProtectedObjects(t *testing.T) {
	before := "Before\n<span discussion-urls=\"thread\">Discussed</span>\n<unknown id=\"block\"/>\nAfter"
	for _, after := range []string{
		"Before changed\n<span discussion-urls=\"thread\">Discussed</span>\n<unknown id=\"block\"/>\nAfter",
		"Before <\n<span discussion-urls=\"thread\">Discussed</span>\n<unknown id=\"block\"/>\nAfter",
	} {
		if !PreservesProtectedObjects(before, after) {
			t.Fatalf("ordinary edit was blocked: %q", after)
		}
	}
	for _, after := range []string{
		"Before\nDiscussed\n<unknown id=\"block\"/>\nAfter",
		"Before\n<span discussion-urls=\"thread\">Discussed</span>\nAfter",
		"Before\n<span discussion-urls=\"other\">Discussed</span>\n<unknown id=\"block\"/>\nAfter",
	} {
		if PreservesProtectedObjects(before, after) {
			t.Fatalf("protected edit was accepted: %q", after)
		}
	}
}

func TestMultipleNearbyDeletionsUseIndependentExactTargets(t *testing.T) {
	base := "Title\nsame\n- item\n<empty-block/>\n<span underline=\"true\">Alpha</span>\n<empty-block/>\n<span underline=\"true\">Beta</span>\nsame\nSection\nsame\n<empty-block/>\n- item\nsame\nTail"
	text := "same\n<span underline=\"true\">Alpha</span>\n<empty-block/>\nSection\nsame\n<empty-block/>\n- item\nsame\nTail"
	updates, err := planContentUpdates(base, text)
	if err != nil || len(updates) < 3 {
		t.Fatalf("safe multi-edit draft was rejected: updates=%d err=%v", len(updates), err)
	}
	applyExactUpdates(t, base, text, updates)
}

func TestInsertionAmongRepeatedEmptyBlocksUsesAdjacentSafeContext(t *testing.T) {
	base := "Unique heading\n<empty-block/>\n<span underline=\"true\">Existing</span>\n<empty-block/>\n<span underline=\"true\">Later</span>\nUnique tail"
	text := "Unique heading\n<empty-block/>\n<empty-block/>\n- [ ] New todo\n<empty-block/>\n<empty-block/>\nNotes\nMore notes\n<span underline=\"true\">Existing</span>\n<empty-block/>\n<span underline=\"true\">Later</span>\nUnique tail"
	updates, err := planContentUpdates(base, text)
	if err != nil || len(updates) != 1 {
		t.Fatalf("ordinary insertion near repeated empty blocks refused: %#v, %v", updates, err)
	}
	applyExactUpdates(t, base, text, updates)
}

func TestProtectedErrorsAreDistinguishableFromAddressingErrors(t *testing.T) {
	_, protectedErr := planContentUpdates("Before\n<unknown id=\"x\"/>\nAfter", "Before\nAfter")
	if !errors.Is(protectedErr, ErrProtectedContent) || !errors.Is(protectedErr, ErrUnsafeUpdate) {
		t.Fatalf("protected change classification: %v", protectedErr)
	}
	_, addressingErr := planContentUpdates("", "new content")
	if !errors.Is(addressingErr, ErrUnsafeUpdate) || errors.Is(addressingErr, ErrProtectedContent) {
		t.Fatalf("ambiguous target classification: %v", addressingErr)
	}
}

func FuzzPlanContentUpdates(f *testing.F) {
	for _, seed := range [][2]string{
		{"prefix café suffix", "prefix cafê suffix"},
		{"a\nb\nc\na\nb\nc", "a\nb changed\nc\na\nb\nc"},
		{"aaaa", "abaa"},
		{"<callout>\n\tBefore\n</callout>", "<callout>\n\tAfter\n</callout>"},
		{"<span color=\"red\">Before</span>", "<span color=\"blue\">After</span>"},
		{"<span\ncolor=\"red\">Before</span>", "<span\ncolor=\"blue\">After</span>"},
		{"<unknown/>\nBefore", "<unknown/>\nAfter"},
		{"", "text"},
		{"\xff", "x"},
	} {
		f.Add(seed[0], seed[1])
	}
	f.Fuzz(func(t *testing.T, base, text string) {
		if len(base)+len(text) > 16_384 {
			t.Skip()
		}
		updates, err := planContentUpdates(base, text)
		if err != nil {
			if !errors.Is(err, ErrUnsafeUpdate) {
				t.Fatal(err)
			}
			return
		}
		applyExactUpdates(t, base, text, updates)
	})
}
