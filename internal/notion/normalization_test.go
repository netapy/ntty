package notion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestEncodeOnlyEditedBlankLinesAsEmptyBlocks(t *testing.T) {
	base := "Before\n\nKeep\n```text\na\n\nb\n```"
	text := "Before\n\nKeep\n```text\na\n\nb\n```\nNew\n\n\nLast\n"
	got, err := encodeEditedEmptyBlocks(base, text)
	want := "Before\n\nKeep\n```text\na\n\nb\n```\nNew\n<empty-block/>\n<empty-block/>\nLast\n<empty-block/>"
	if err != nil || got != want {
		t.Fatalf("empty block upgrade:\n got %q\nwant %q\nerr %v", got, want, err)
	}
	if !strings.Contains(got, "Before\n\nKeep") || !strings.Contains(got, "a\n\nb") {
		t.Fatal("untouched prose or code blank line changed")
	}
}

func TestTransientNotionFileURLsRebaseWithoutConflict(t *testing.T) {
	oldURL := "https://prod-files-secure.s3.us-west-2.amazonaws.com/workspace/image.png?X-Amz-Algorithm=old&X-Amz-Signature=old"
	draftURL := "https://prod-files-secure.s3.us-west-2.amazonaws.com/workspace/image.png?X-Amz-Algorithm=draft&X-Amz-Signature=draft"
	newURL := "https://prod-files-secure.s3.us-west-2.amazonaws.com/workspace/image.png?X-Amz-Algorithm=new&X-Amz-Signature=new"
	base := "Old top\n![image](" + oldURL + ")\nEnd"
	text := "New top\n![image](" + draftURL + ")\nEnd"
	remote := "Old top\n![image](" + newURL + ")\nEnd"
	want := "New top\n![image](" + newURL + ")\nEnd"

	c := NewClient()
	c.interval = 0
	writes := 0
	c.run = func(_ context.Context, args []string, body []byte) ([]byte, error) {
		if args[3] == "GET" {
			return json.Marshal(Content{Object: "page_markdown", Markdown: remote})
		}
		writes++
		var request struct {
			Update struct {
				Updates []contentUpdate `json:"content_updates"`
			} `json:"update_content"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			return nil, err
		}
		for _, update := range request.Update.Updates {
			if strings.Contains(update.OldStr+update.NewStr, "X-Amz-") {
				t.Fatal("save tried to restore an expired file URL")
			}
			remote = strings.Replace(remote, update.OldStr, update.NewStr, 1)
		}
		return json.Marshal(Content{Object: "page_markdown", Markdown: remote})
	}
	result, err := c.Save(context.Background(), "page", base, text)
	if err != nil || result.Markdown != want || !result.Normalized || writes != 1 {
		t.Fatalf("transient URL save: %+v err=%v writes=%d", result, err, writes)
	}
}

func TestNamedAndCanonicalUserMentionsAreTheSameSavedText(t *testing.T) {
	named := `Hello <mention-user url="{{user://12345678-1234-4234-8234-123456789abc}}">Alex</mention-user>`
	canonical := `Hello <mention-user url="user://12345678-1234-4234-8234-123456789abc"/>`
	if !sameSavedText(named, canonical) {
		t.Fatal("Notion's canonical user mention was treated as changed content")
	}
	other := `Hello <mention-user url="user://2290ac2e-0555-4cd3-b1d5-97fd43e29ac8"/>`
	if sameSavedText(named, other) {
		t.Fatal("different user identities were normalized together")
	}
}

func TestSavedTextAcceptsNotionTrailingSpaceNormalization(t *testing.T) {
	if !sameSavedText("- [ ] ", "- [ ]") {
		t.Fatal("empty task trailing-space normalization was treated as a conflict")
	}
	if sameSavedText("```\nvalue  \n```", "```\nvalue\n```") {
		t.Fatal("code-block trailing spaces were incorrectly ignored")
	}
}

func TestSaveAcceptsNotionsCanonicalUserMention(t *testing.T) {
	base := "Hello"
	named := `Hello <mention-user url="{{user://12345678-1234-4234-8234-123456789abc}}">Alex</mention-user>`
	canonical := `Hello <mention-user url="user://12345678-1234-4234-8234-123456789abc"/>`
	c := NewClient()
	c.interval = 0
	c.run = func(_ context.Context, args []string, _ []byte) ([]byte, error) {
		markdown := base
		if args[3] == "PATCH" {
			markdown = canonical
		}
		return json.Marshal(Content{Object: "page_markdown", ID: "page", Markdown: markdown})
	}
	result, err := c.Save(context.Background(), "page", base, named)
	if err != nil || result.Markdown != canonical || !result.Normalized {
		t.Fatalf("canonical mention save: result=%+v err=%v", result, err)
	}
}

func TestTransientNotionURLIdentityMatrix(t *testing.T) {
	urls := func(host, path, signature string) string {
		return "https://" + host + "/" + path + "?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Signature=" + signature + "&X-Amz-Expires=3600"
	}
	for _, host := range []string{
		"prod-files-secure.s3.us-west-2.amazonaws.com",
		"prod-files-secure.s3.eu-west-1.amazonaws.com",
		"file.notion.so",
		"secure.notion-static.com",
	} {
		t.Run(host, func(t *testing.T) {
			oldURL := urls(host, "workspace/file.png", "old")
			draftURL := urls(host, "workspace/file.png", "draft")
			newURL := urls(host, "workspace/file.png", "new")
			base := "Before\n![file](" + oldURL + ")\nAfter"
			text := "Before local\n![file](" + draftURL + ")\nAfter"
			remote := "Before\n![file](" + newURL + ")\nAfter remote"
			alignedBase, alignedText := alignTransientNotionURLs(base, text, remote)
			if strings.Contains(alignedBase+alignedText, "Signature=old") || strings.Contains(alignedBase+alignedText, "Signature=draft") || !strings.Contains(alignedBase, newURL) || !strings.Contains(alignedText, newURL) {
				t.Fatalf("signatures not aligned:\nbase %q\ntext %q", alignedBase, alignedText)
			}
			merged, ok, err := mergeNonOverlapping(alignedBase, alignedText, remote)
			if err != nil || !ok || !strings.Contains(merged, "Before local") || !strings.Contains(merged, "After remote") {
				t.Fatalf("URL plus prose merge failed: %q, %v, %v", merged, ok, err)
			}
		})
	}
	base := "![file](" + urls("file.notion.so", "workspace/a.png", "old") + ")"
	other := "![file](" + urls("file.notion.so", "workspace/b.png", "new") + ")"
	alignedBase, _ := alignTransientNotionURLs(base, base, other)
	if alignedBase != base {
		t.Fatal("different stable file identity was rebased")
	}
	unsigned := "https://file.notion.so/workspace/file.png?download=1"
	if stableNotionMarkdown(unsigned) != unsigned {
		t.Fatal("unsigned URL was normalized")
	}
}

func TestThousandsOfRotatingSignaturesRemainOneFile(t *testing.T) {
	const stable = "https://prod-files-secure.s3.us-west-2.amazonaws.com/workspace/image.png"
	for i := 0; i < 5_000; i++ {
		oldURL := fmt.Sprintf("%s?X-Amz-Signature=old-%d&X-Amz-Algorithm=AWS4-HMAC-SHA256", stable, i)
		draftURL := fmt.Sprintf("%s?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Signature=draft-%d", stable, i)
		newURL := fmt.Sprintf("%s?X-Amz-Expires=%d&X-Amz-Signature=new-%d", stable, i+1, i)
		base := "![image](" + oldURL + ")"
		text := "Caption changed\n![image](" + draftURL + ")"
		remote := "![image](" + newURL + ")"
		alignedBase, alignedText := alignTransientNotionURLs(base, text, remote)
		if alignedBase != remote || alignedText != "Caption changed\n"+remote {
			t.Fatalf("iteration %d: %q / %q", i, alignedBase, alignedText)
		}
	}
}

func TestMergeNonOverlappingLocalAndRemoteEdits(t *testing.T) {
	base := "First\nSecond\nThird"
	local := "First locally\nSecond\nThird"
	remote := "First\nSecond\nThird remotely"
	merged, ok, err := mergeNonOverlapping(base, local, remote)
	if err != nil || !ok || merged != "First locally\nSecond\nThird remotely" {
		t.Fatalf("disjoint merge: %q, %v, %v", merged, ok, err)
	}
	if merged, ok, err := mergeNonOverlapping(base, "First local\nSecond\nThird", "First remote\nSecond\nThird"); err != nil || !ok || merged != "First local remote\nSecond\nThird" {
		t.Fatalf("concurrent same-point insertions were not retained: %q, %v, %v", merged, ok, err)
	}
	merged, ok, err = mergeNonOverlapping(base, "First\nSecond\nThird\nLocal", "Remote\nFirst\nSecond\nThird")
	if err != nil || !ok || merged != "Remote\nFirst\nSecond\nThird\nLocal" {
		t.Fatalf("opposite-end insertions: %q, %v, %v", merged, ok, err)
	}
}

func TestMergeIndependentEditsWithinSameParagraph(t *testing.T) {
	for _, test := range []struct {
		base, local, remote, want string
	}{
		{"The quick brown fox", "The swift brown fox", "The quick brown wolf", "The swift brown wolf"},
		{"Alpha middle Omega", "Alpha local middle Omega", "Alpha middle remote Omega", "Alpha local middle remote Omega"},
		{"Café déjà terminé", "Café enfin déjà terminé", "Café déjà fini", "Café enfin déjà fini"},
		{"Task", "Task local", "Task remote", "Task local remote"},
		{"Hello", "Hello world", "Hello!", "Hello world!"},
	} {
		got, ok, err := mergeNonOverlapping(test.base, test.local, test.remote)
		if err != nil || !ok || got != test.want {
			t.Fatalf("same-paragraph merge: got=%q ok=%v err=%v; want=%q", got, ok, err, test.want)
		}
		reverse, reverseOK, reverseErr := mergeNonOverlapping(test.base, test.remote, test.local)
		if reverseErr != nil || !reverseOK || reverse != test.want {
			t.Fatalf("same-paragraph merge was asymmetric: %q, %v, %v", reverse, reverseOK, reverseErr)
		}
	}
}

func TestMergeInsertionDeletionAndDocumentBoundaryMatrix(t *testing.T) {
	cases := []struct {
		name, base, local, remote, want string
		ok                              bool
	}{
		{"same insertion", "A\nB", "A\nX\nB", "A\nX\nB", "A\nX\nB", true},
		{"different insertion same point", "A\nB", "A\nL\nB", "A\nR\nB", "", false},
		{"insert middle and edit first", "A\nB\nC", "A\nB\nLocal\nC", "Remote\nB\nC", "Remote\nB\nLocal\nC", true},
		{"delete final predecessor and edit final", "A\nB\nC", "A\nC", "A\nB\nC remote", "A\nC remote", true},
		{"delete first and edit last", "A\nB\nC", "B\nC", "A\nB\nC remote", "B\nC remote", true},
		{"opposite boundaries", "A\nB", "Local\nA\nB", "A\nB\nRemote", "Local\nA\nB\nRemote", true},
		{"same block insertions", "A\nB", "A local\nB", "A remote\nB", "A local remote\nB", true},
		{"trailing newline local", "A\nB", "A\nB\n", "A remote\nB", "A remote\nB\n", true},
		{"trailing newline and remote insertion", "A\nB", "A\nB\n", "A\nB\nRemote", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok, err := mergeNonOverlapping(tc.base, tc.local, tc.remote)
			if err != nil || ok != tc.ok || got != tc.want {
				t.Fatalf("merge: got=%q ok=%v err=%v; want=%q ok=%v", got, ok, err, tc.want, tc.ok)
			}
			if ok {
				reverse, reverseOK, reverseErr := mergeNonOverlapping(tc.base, tc.remote, tc.local)
				if reverseErr != nil || !reverseOK || reverse != got {
					t.Fatalf("asymmetric merge: %q, %v, %v", reverse, reverseOK, reverseErr)
				}
			}
		})
	}
}

func FuzzMergeNonOverlapping(f *testing.F) {
	for _, seed := range [][3]string{
		{"A\nB\nC", "A local\nB\nC", "A\nB\nC remote"},
		{"A\nB\nC", "A\nC", "A\nB\nC remote"},
		{"same\nsame\nsame", "same\nlocal\nsame", "remote\nsame\nsame"},
		{"<empty-block/>", "local", "remote"},
	} {
		f.Add(seed[0], seed[1], seed[2])
	}
	f.Fuzz(func(t *testing.T, base, local, remote string) {
		if base == "" || len(base)+len(local)+len(remote) > 32<<10 || !utf8.ValidString(base+local+remote) {
			t.Skip()
		}
		merged, ok, err := mergeNonOverlapping(base, local, remote)
		if err != nil {
			if !errors.Is(err, ErrUnsafeUpdate) {
				t.Fatal(err)
			}
			return
		}
		reverse, reverseOK, reverseErr := mergeNonOverlapping(base, remote, local)
		if reverseErr != nil || reverseOK != ok || reverse != merged {
			t.Fatalf("asymmetric merge: forward=(%q,%v) reverse=(%q,%v,%v)", merged, ok, reverse, reverseOK, reverseErr)
		}
		if !ok {
			return
		}
		// A successful save retains remote as its new base and merged as its exact
		// attempted text, so verify that pair can always form a safe PATCH. Ambiguous
		// repeated-line outputs are correctly rejected by the exact planner.
		if updates, planErr := planContentUpdates(remote, merged); planErr == nil {
			applyExactUpdates(t, remote, merged, updates)
		}
	})
}

func TestSaveMergesIndependentRemoteBlock(t *testing.T) {
	base := "Top\n<unknown id=\"old\"/>\nBottom"
	local := "Top locally\n<unknown id=\"old\"/>\nBottom"
	remote := "Top\n<unknown id=\"new\"/>\nBottom"
	want := "Top locally\n<unknown id=\"new\"/>\nBottom"
	c := NewClient()
	c.interval = 0
	writes := 0
	c.run = func(_ context.Context, args []string, body []byte) ([]byte, error) {
		if args[3] == "GET" {
			return json.Marshal(Content{Object: "page_markdown", Markdown: remote})
		}
		writes++
		var request struct {
			Update struct {
				Updates []contentUpdate `json:"content_updates"`
			} `json:"update_content"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			return nil, err
		}
		for _, update := range request.Update.Updates {
			remote = strings.Replace(remote, update.OldStr, update.NewStr, 1)
		}
		return json.Marshal(Content{Object: "page_markdown", Markdown: remote})
	}
	result, err := c.Save(context.Background(), "page", base, local)
	if err != nil || writes != 1 || result.Markdown != want {
		t.Fatalf("merged save: %+v, err=%v, writes=%d", result, err, writes)
	}
}

func TestSaveReconcilesLocalEditAlreadyPresentWithRemoteAddition(t *testing.T) {
	base := "First old\nMiddle\nLast old"
	local := "First local\nMiddle\nLast old"
	remote := "First local\nMiddle\nLast remote"
	c := NewClient()
	c.interval = 0
	calls := 0
	c.run = func(_ context.Context, args []string, _ []byte) ([]byte, error) {
		calls++
		if args[3] != "GET" {
			t.Fatal("reconciled merge sent an empty PATCH")
		}
		return json.Marshal(Content{Object: "page_markdown", Markdown: remote})
	}
	result, err := c.Save(context.Background(), "page", base, local)
	if err != nil || calls != 1 || result.Markdown != remote || !result.Normalized {
		t.Fatalf("reconciliation: %+v err=%v calls=%d", result, err, calls)
	}
}

func TestSaveAcceptsIndependentRemoteEditDuringPatch(t *testing.T) {
	base := "First old\nMiddle\nLast old"
	local := "First local\nMiddle\nLast old"
	want := "First local\nMiddle\nLast remote"
	c := NewClient()
	c.interval = 0
	calls := 0
	c.run = func(_ context.Context, args []string, _ []byte) ([]byte, error) {
		calls++
		if args[3] == "GET" {
			return json.Marshal(Content{Object: "page_markdown", Markdown: base})
		}
		return json.Marshal(Content{Object: "page_markdown", Markdown: want})
	}
	result, err := c.Save(context.Background(), "page", base, local)
	if err != nil || calls != 2 || result.Markdown != want || !result.Normalized {
		t.Fatalf("concurrent independent edit: %+v err=%v calls=%d", result, err, calls)
	}
}

func TestSaveLostPatchResponseReconcilesOnNextAttempt(t *testing.T) {
	base := "Before\nMiddle\nAfter"
	local := "Before local\nMiddle\nAfter"
	remote := base
	c := NewClient()
	c.interval = 0
	reads, writes := 0, 0
	c.run = func(_ context.Context, args []string, body []byte) ([]byte, error) {
		if args[3] == "GET" {
			reads++
			return json.Marshal(Content{Object: "page_markdown", Markdown: remote})
		}
		writes++
		var request struct {
			Update struct {
				Updates []contentUpdate `json:"content_updates"`
			} `json:"update_content"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			return nil, err
		}
		for _, update := range request.Update.Updates {
			remote = strings.Replace(remote, update.OldStr, update.NewStr, 1)
		}
		return nil, fmt.Errorf("503 response lost")
	}
	_, firstErr := c.Save(context.Background(), "page", base, local)
	var attempt *SaveAttemptError
	if firstErr == nil || !errors.As(firstErr, &attempt) || writes != 1 {
		t.Fatalf("ambiguous first write lost retry state: err=%v writes=%d", firstErr, writes)
	}
	result, err := c.Save(context.Background(), "page", attempt.Base.Markdown, attempt.Text)
	if err != nil || result.Markdown != local || reads != 2 || writes != 1 {
		t.Fatalf("lost response not reconciled: %+v err=%v reads=%d writes=%d", result, err, reads, writes)
	}
}

func TestRebaseDraftKeepsTypingMadeDuringSave(t *testing.T) {
	tests := []struct {
		name                  string
		saved, latest, remote string
		want                  string
		ok                    bool
	}{
		{
			name:   "independent remote line",
			saved:  "First saved\nMiddle\nLast",
			latest: "First saved\nMiddle\nLast typed",
			remote: "First saved\nMiddle remote\nLast",
			want:   "First saved\nMiddle remote\nLast typed",
			ok:     true,
		},
		{
			name:   "canonical user mention",
			saved:  `Hello <mention-user url="{{user://person}}">Ada</mention-user>`,
			latest: `Hello <mention-user url="{{user://person}}">Ada</mention-user>!`,
			remote: `Hello <mention-user url="user://person"/>`,
			want:   `Hello <mention-user url="user://person"/>!`,
			ok:     true,
		},
		{
			name:   "same line conflict",
			saved:  "Original",
			latest: "Local",
			remote: "Remote",
			ok:     false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok, err := RebaseDraft(test.saved, test.latest, test.remote)
			if err != nil || ok != test.ok || ok && got != test.want {
				t.Fatalf("RebaseDraft() = %q, %v, %v; want %q, %v", got, ok, err, test.want, test.ok)
			}
		})
	}
}

func TestComplexMergedWriteSurvivesLostResponse(t *testing.T) {
	base := "\n\nC\n"
	local := "0\n0\nC"
	remote := "0\n\n"
	c := NewClient()
	c.interval = 0
	writes := 0
	c.run = func(_ context.Context, args []string, body []byte) ([]byte, error) {
		if args[3] == "GET" {
			return json.Marshal(Content{Object: "page_markdown", Markdown: remote})
		}
		writes++
		var request struct {
			Update struct {
				Updates []contentUpdate `json:"content_updates"`
			} `json:"update_content"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			return nil, err
		}
		for _, update := range request.Update.Updates {
			remote = strings.Replace(remote, update.OldStr, update.NewStr, 1)
		}
		return nil, fmt.Errorf("503 response lost")
	}
	_, firstErr := c.Save(context.Background(), "page", base, local)
	var attempt *SaveAttemptError
	if !errors.As(firstErr, &attempt) || attempt.Base.Markdown != "0\n\n" || attempt.Text != "0\n0\n" || writes != 1 {
		t.Fatalf("wrong retained merge: %#v err=%v writes=%d", attempt, firstErr, writes)
	}
	result, err := c.Save(context.Background(), "page", attempt.Base.Markdown, attempt.Text)
	if err != nil || result.Markdown != attempt.Text || writes != 1 {
		t.Fatalf("merged retry repeated or conflicted: %+v err=%v writes=%d", result, err, writes)
	}
}

func TestMergeThousandsOfDisjointBlockEdits(t *testing.T) {
	rng := rand.New(rand.NewSource(20260920))
	for iteration := 0; iteration < 5_000; iteration++ {
		count := 3 + rng.Intn(20)
		baseLines := make([]string, count)
		localLines := make([]string, count)
		remoteLines := make([]string, count)
		wantLines := make([]string, count)
		for i := range count {
			baseLines[i] = fmt.Sprintf("block-%02d original-%d", i, iteration)
			localLines[i], remoteLines[i], wantLines[i] = baseLines[i], baseLines[i], baseLines[i]
			switch rng.Intn(4) {
			case 0:
				localLines[i] = fmt.Sprintf("block-%02d local-%d", i, iteration)
				wantLines[i] = localLines[i]
			case 1:
				remoteLines[i] = fmt.Sprintf("block-%02d remote-%d", i, iteration)
				wantLines[i] = remoteLines[i]
			case 2:
				// Both sides made the same edit, as after a lost response.
				localLines[i] = fmt.Sprintf("block-%02d shared-%d", i, iteration)
				remoteLines[i] = localLines[i]
				wantLines[i] = localLines[i]
			}
		}
		base := strings.Join(baseLines, "\n")
		local := strings.Join(localLines, "\n")
		remote := strings.Join(remoteLines, "\n")
		want := strings.Join(wantLines, "\n")
		merged, ok, err := mergeNonOverlapping(base, local, remote)
		if err != nil || !ok || merged != want {
			t.Fatalf("iteration %d: ok=%v err=%v\n got %q\nwant %q", iteration, ok, err, merged, want)
		}
		updates, err := planContentUpdates(remote, merged)
		if err != nil {
			t.Fatalf("iteration %d merged result cannot sync: %v", iteration, err)
		}
		applyExactUpdates(t, remote, merged, updates)
	}
}

func TestMergeRetainsThousandsOfConcurrentBlockAppends(t *testing.T) {
	for iteration := 0; iteration < 5_000; iteration++ {
		line := iteration % 7
		baseLines := []string{"zero", "one", "two", "three", "four", "five", "six"}
		localLines := append([]string{}, baseLines...)
		remoteLines := append([]string{}, baseLines...)
		localLines[line] += fmt.Sprintf(" local-%d", iteration)
		remoteLines[line] += fmt.Sprintf(" remote-%d", iteration)
		merged, ok, err := mergeNonOverlapping(strings.Join(baseLines, "\n"), strings.Join(localLines, "\n"), strings.Join(remoteLines, "\n"))
		wantLines := append([]string{}, baseLines...)
		wantLines[line] += fmt.Sprintf(" local-%d remote-%d", iteration, iteration)
		want := strings.Join(wantLines, "\n")
		if err != nil || !ok || merged != want {
			t.Fatalf("iteration %d concurrent appends were not retained: got=%q ok=%v err=%v want=%q", iteration, merged, ok, err, want)
		}
	}
}

func TestMergeThousandsOfDeletionsBesideRemoteEdits(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for iteration := 0; iteration < 5_000; iteration++ {
		count := 5 + rng.Intn(20)
		baseLines := make([]string, count)
		deleted := make([]bool, count)
		remoteLines := make([]string, count)
		for i := range count {
			baseLines[i] = fmt.Sprintf("unique block %02d iteration %d", i, iteration)
			remoteLines[i] = baseLines[i]
			if rng.Intn(5) == 0 {
				deleted[i] = true
			} else if rng.Intn(4) == 0 {
				remoteLines[i] += " remote"
			}
		}
		var localLines, wantLines []string
		for i := range count {
			if !deleted[i] {
				localLines = append(localLines, baseLines[i])
				wantLines = append(wantLines, remoteLines[i])
			}
		}
		base := strings.Join(baseLines, "\n")
		local := strings.Join(localLines, "\n")
		remote := strings.Join(remoteLines, "\n")
		want := strings.Join(wantLines, "\n")
		merged, ok, err := mergeNonOverlapping(base, local, remote)
		if err != nil || !ok || merged != want {
			le, _ := diffMarkdownLines(base, local)
			re, _ := diffMarkdownLines(base, remote)
			t.Fatalf("iteration %d local-delete merge: ok=%v err=%v\nlocal edits=%#v\nremote edits=%#v\n got %q\nwant %q", iteration, ok, err, le, re, merged, want)
		}
		// The algorithm must not depend on which side made the deletion.
		reverse, ok, err := mergeNonOverlapping(base, remote, local)
		if err != nil || !ok || reverse != want {
			t.Fatalf("iteration %d remote-delete merge: ok=%v err=%v\n got %q\nwant %q", iteration, ok, err, reverse, want)
		}
		updates, err := planContentUpdates(remote, merged)
		if err != nil {
			t.Fatalf("iteration %d deletion merge cannot sync: %v", iteration, err)
		}
		applyExactUpdates(t, remote, merged, updates)
	}
}

func TestPartialEmptyPageWriteContinuesWithoutDeletingPrefix(t *testing.T) {
	base := "<empty-block/>"
	text := "Hello\n\n\nNext\n"
	remote := "Hello\n<empty-block/>"
	want := "Hello\n<empty-block/>\n<empty-block/>\nNext\n<empty-block/>"

	c := NewClient()
	c.interval = 0
	c.run = func(_ context.Context, args []string, body []byte) ([]byte, error) {
		if args[3] == "GET" {
			return json.Marshal(Content{Object: "page_markdown", Markdown: remote})
		}
		var request struct {
			Update struct {
				Updates []contentUpdate `json:"content_updates"`
				Delete  bool            `json:"allow_deleting_content"`
			} `json:"update_content"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			return nil, err
		}
		if request.Update.Delete {
			t.Fatal("partial-write recovery enabled deletion")
		}
		for _, update := range request.Update.Updates {
			if !strings.Contains(remote, update.OldStr) {
				t.Fatalf("non-exact recovery target: %#v", update)
			}
			remote = strings.Replace(remote, update.OldStr, update.NewStr, 1)
		}
		return json.Marshal(Content{Object: "page_markdown", Markdown: remote})
	}
	result, err := c.Save(context.Background(), "page", base, text)
	if err != nil || result.Markdown != want || !result.Normalized {
		t.Fatalf("partial empty-page recovery: %+v err=%v", result, err)
	}
}
