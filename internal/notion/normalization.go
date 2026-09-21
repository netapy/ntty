package notion

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// Older ntty builds represented newly inserted empty Notion blocks as blank
// Markdown lines. Notion may stop importing after such a run. Upgrade only
// blank lines inside edited ranges; untouched source remains byte-for-byte.
func encodeEditedEmptyBlocks(base, text string) (string, error) {
	changes, err := diffMarkdownLines(base, text)
	if err != nil || len(changes) == 0 {
		return text, err
	}
	type replacement struct {
		start, end int
		text       string
	}
	var replacements []replacement
	delta := 0
	for _, change := range changes {
		start := change.start + delta
		end := start + len(change.text)
		encoded := encodeBlankLines(change.text, markdownFenceAt(text[:start]), end == len(text))
		if encoded != change.text {
			replacements = append(replacements, replacement{start, end, encoded})
		}
		delta += len(change.text) - (change.end - change.start)
	}
	for i := len(replacements) - 1; i >= 0; i-- {
		r := replacements[i]
		text = text[:r.start] + r.text + text[r.end:]
	}
	return text, nil
}

func markdownFenceAt(markdown string) string {
	fence := ""
	for _, line := range strings.Split(markdown, "\n") {
		trim := strings.TrimSpace(line)
		if !strings.HasPrefix(trim, "```") && !strings.HasPrefix(trim, "~~~") {
			continue
		}
		marker := trim[:3]
		if fence == "" {
			fence = marker
		} else if strings.HasPrefix(trim, fence) {
			fence = ""
		}
	}
	return fence
}

func encodeBlankLines(markdown, fence string, documentEnd bool) string {
	var out strings.Builder
	for len(markdown) > 0 {
		end := strings.IndexByte(markdown, '\n')
		line, separator := markdown, ""
		if end >= 0 {
			line, separator, markdown = markdown[:end], "\n", markdown[end+1:]
		} else {
			markdown = ""
		}
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~") {
			marker := trim[:3]
			if fence == "" {
				fence = marker
			} else if strings.HasPrefix(trim, fence) {
				fence = ""
			}
		}
		if line == "" && fence == "" {
			line = "<empty-block/>"
		}
		out.WriteString(line)
		out.WriteString(separator)
	}
	if documentEnd && out.Len() > 0 && strings.HasSuffix(out.String(), "\n") {
		out.WriteString("<empty-block/>")
	}
	return out.String()
}

var markdownURL = regexp.MustCompile(`https?://[^\s<>"')\]]+`)
var notionUserMention = regexp.MustCompile(`<mention-user\s+url\s*=\s*"(?:\{\{)?user://([0-9A-Za-z-]+)(?:\}\})?"\s*(?:/>|>[^<]*</mention-user\s*>)`)

func transientNotionFileURL(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.RawQuery == "" {
		return "", false
	}
	host := strings.ToLower(u.Hostname())
	knownHost := host == "file.notion.so" || host == "secure.notion-static.com" ||
		strings.HasPrefix(host, "prod-files-secure.s3.") && strings.HasSuffix(host, ".amazonaws.com")
	query := u.Query()
	signed := query.Get("X-Amz-Signature") != "" || query.Get("X-Amz-Algorithm") != "" || query.Get("token") != ""
	if !knownHost || !signed {
		return "", false
	}
	u.RawQuery = ""
	return u.String(), true
}

func stableNotionMarkdown(markdown string) string {
	markdown = canonicalPageMentions(markdown)
	markdown = markdownURL.ReplaceAllStringFunc(markdown, func(raw string) string {
		if stable, ok := transientNotionFileURL(raw); ok {
			return stable
		}
		return raw
	})
	return notionUserMention.ReplaceAllString(markdown, `<mention-user url="user://$1"/>`)
}

func rebaseTransientNotionURLs(base, text, remote string) (string, bool) {
	if stableNotionMarkdown(base) != stableNotionMarkdown(remote) {
		return text, false
	}
	_, text = alignTransientNotionURLs(base, text, remote)
	return text, true
}

func collectTransientNotionURLs(markdown string) map[string]map[string]struct{} {
	found := map[string]map[string]struct{}{}
	for _, raw := range markdownURL.FindAllString(markdown, -1) {
		stable, ok := transientNotionFileURL(raw)
		if !ok {
			continue
		}
		if found[stable] == nil {
			found[stable] = map[string]struct{}{}
		}
		found[stable][raw] = struct{}{}
	}
	return found
}

// Align every cached spelling of a transient URL with the current remote one.
// This also handles a recovered draft that carries a third valid signature.
func alignTransientNotionURLs(base, text, remote string) (string, string) {
	base, text = alignPageMentions(base, text, remote)
	baseURLs := collectTransientNotionURLs(base)
	remoteURLs := collectTransientNotionURLs(remote)
	align := func(markdown string) string {
		for stable, spellings := range collectTransientNotionURLs(markdown) {
			newSet := remoteURLs[stable]
			if len(baseURLs[stable]) == 0 || len(newSet) != 1 {
				continue
			}
			var current string
			for current = range newSet {
			}
			for spelling := range spellings {
				markdown = strings.ReplaceAll(markdown, spelling, current)
			}
		}
		return markdown
	}
	return align(base), align(text)
}

// mergeNonOverlapping combines local and remote edits made from the same base.
// It first merges independent blocks, then independent rune ranges inside a
// shared paragraph. Only genuinely competing replacements remain conflicts.
func mergeNonOverlapping(base, local, remote string) (string, bool, error) {
	forward, ok, err := mergeNonOverlappingCore(base, local, remote)
	if err != nil || !ok {
		return forward, ok, err
	}
	reverse, reverseOK, reverseErr := mergeNonOverlappingCore(base, remote, local)
	if reverseErr != nil {
		return "", false, reverseErr
	}
	if !reverseOK || reverse != forward {
		return "", false, nil
	}
	return forward, true, nil
}

// RebaseDraft applies edits made after a save started to the content Notion
// confirmed for that save. Independent lines and rune ranges merge, equivalent
// mention/URL canonicalization is ignored, and a genuinely competing
// replacement is returned as a conflict instead of discarding either version.
func RebaseDraft(saved, latest, remote string) (string, bool, error) {
	if latest == saved {
		return remote, true, nil
	}
	saved, latest = alignTransientNotionURLs(saved, latest, remote)
	saved, latest, remote = canonicalTables(saved), canonicalTables(latest), canonicalTables(remote)
	canonicalMentions := func(markdown string) string {
		return notionUserMention.ReplaceAllString(markdown, `<mention-user url="user://$1"/>`)
	}
	saved = canonicalMentions(saved)
	latest = canonicalMentions(latest)
	remote = canonicalMentions(remote)
	if saved == remote {
		return latest, true, nil
	}
	return mergeNonOverlapping(saved, latest, remote)
}

func mergeNonOverlappingCore(base, local, remote string) (string, bool, error) {
	baseLines := strings.Split(base, "\n")
	localLines := strings.Split(local, "\n")
	remoteLines := strings.Split(remote, "\n")
	if merged, ok := mergeCorrespondingLines(baseLines, localLines, remoteLines); ok {
		return strings.Join(merged, "\n"), true, nil
	}
	localEdits, err := diffLineSequence(baseLines, localLines)
	if err != nil {
		return "", false, err
	}
	remoteEdits, err := diffLineSequence(baseLines, remoteLines)
	if err != nil {
		return "", false, err
	}
	// A previous response may have been lost after deletions landed. Remove pure
	// local deletions from the old base and retry line correspondence against the
	// shorter remote document before treating adjacent remote edits as overlap.
	for _, edits := range [][]lineSequenceEdit{localEdits, remoteEdits} {
		reduced := append([]string{}, baseLines...)
		changed := false
		for i := len(edits) - 1; i >= 0; i-- {
			edit := edits[i]
			if len(edit.lines) != 0 || edit.start == edit.end {
				continue
			}
			reduced = append(reduced[:edit.start], reduced[edit.end:]...)
			changed = true
		}
		if changed {
			if merged, ok := mergeCorrespondingLines(reduced, localLines, remoteLines); ok {
				return strings.Join(merged, "\n"), true, nil
			}
		}
	}
	type taggedEdit struct {
		lineSequenceEdit
		local bool
	}
	all := make([]taggedEdit, 0, len(localEdits)+len(remoteEdits))
	remoteConsumed := make([]bool, len(remoteEdits))
	for _, edit := range localEdits {
		duplicate := false
		for i, other := range remoteEdits {
			if edit.start == other.start && edit.end == other.end && slicesEqual(edit.lines, other.lines) {
				remoteConsumed[i] = true
				all = append(all, taggedEdit{lineSequenceEdit: edit, local: true})
				duplicate = true
				break
			}
			if combined, ok := combineBoundaryInsertion(edit, other); ok {
				remoteConsumed[i] = true
				all = append(all, taggedEdit{lineSequenceEdit: combined, local: true})
				duplicate = true
				break
			}
			if lineSequenceEditsOverlap(edit, other) {
				if combined, ok := combineContainedLineEdits(baseLines, edit, other); ok {
					remoteConsumed[i] = true
					all = append(all, taggedEdit{lineSequenceEdit: combined, local: true})
					duplicate = true
					break
				}
				return "", false, nil
			}
		}
		if !duplicate {
			all = append(all, taggedEdit{lineSequenceEdit: edit, local: true})
		}
	}
	for i, edit := range remoteEdits {
		if !remoteConsumed[i] {
			all = append(all, taggedEdit{lineSequenceEdit: edit})
		}
	}
	for i, edit := range all {
		if edit.start < 0 || edit.end < edit.start || edit.end > len(baseLines) {
			return "", false, nil
		}
		for j := i + 1; j < len(all); j++ {
			if lineSequenceEditsOverlap(edit.lineSequenceEdit, all[j].lineSequenceEdit) {
				return "", false, nil
			}
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].start != all[j].start {
			return all[i].start > all[j].start
		}
		if all[i].end != all[j].end {
			return all[i].end > all[j].end
		}
		return !all[i].local && all[j].local
	})
	mergedLines := append([]string{}, baseLines...)
	for _, edit := range all {
		if edit.start > len(mergedLines) || edit.end > len(mergedLines) {
			return "", false, nil
		}
		next := make([]string, 0, len(mergedLines)-(edit.end-edit.start)+len(edit.lines))
		next = append(next, mergedLines[:edit.start]...)
		next = append(next, edit.lines...)
		next = append(next, mergedLines[edit.end:]...)
		mergedLines = next
	}
	return strings.Join(mergedLines, "\n"), true, nil
}

func combineBoundaryInsertion(a, b lineSequenceEdit) (lineSequenceEdit, bool) {
	if a.start == a.end && b.start == b.end && a.start == b.start {
		// Concurrent new blocks do not compete for existing text. Retain both
		// additions; a subset is already represented (common with blank blocks
		// and an earlier write whose response was lost).
		if lineSubsequence(a.lines, b.lines) {
			return b, true
		}
		if lineSubsequence(b.lines, a.lines) {
			return a, true
		}
		// Distinct additions at the same point may be an unrecognized canonical
		// response to our own write. Combining them makes every retry append
		// another copy. Keep both versions as a conflict rather than guessing.
		return lineSequenceEdit{}, false
	}
	container, insertion := a, b
	if insertion.start != insertion.end {
		container, insertion = b, a
	}
	if insertion.start != insertion.end || len(insertion.lines) == 0 || container.start == container.end {
		return lineSequenceEdit{}, false
	}
	if insertion.start == container.start && len(container.lines) > len(insertion.lines) && slicesEqual(container.lines[:len(insertion.lines)], insertion.lines) {
		return container, true
	}
	if insertion.start == container.end && len(container.lines) > len(insertion.lines) && slicesEqual(container.lines[len(container.lines)-len(insertion.lines):], insertion.lines) {
		return container, true
	}
	return lineSequenceEdit{}, false
}

func combineContainedLineEdits(base []string, a, b lineSequenceEdit) (lineSequenceEdit, bool) {
	container, inner := a, b
	if !(container.start <= inner.start && container.end >= inner.end) {
		container, inner = b, a
		if !(container.start <= inner.start && container.end >= inner.end) {
			return lineSequenceEdit{}, false
		}
	}
	segment := append([]string{}, base[container.start:container.end]...)
	start, end := inner.start-container.start, inner.end-container.start
	next := make([]string, 0, len(segment)-(end-start)+len(inner.lines))
	next = append(next, segment[:start]...)
	next = append(next, inner.lines...)
	next = append(next, segment[end:]...)
	if !lineSubsequence(container.lines, next) || len(inner.lines) > 0 && !lineSubsequence(inner.lines, container.lines) {
		return lineSequenceEdit{}, false
	}
	return container, true
}

func lineSubsequence(needle, haystack []string) bool {
	at := 0
	for _, line := range haystack {
		if at < len(needle) && needle[at] == line {
			at++
		}
	}
	return at == len(needle)
}

func mergeCorrespondingLines(base, local, remote []string) ([]string, bool) {
	if len(base) != len(local) || len(base) != len(remote) {
		return nil, false
	}
	merged := make([]string, len(base))
	for i, original := range base {
		switch {
		case local[i] == remote[i]:
			merged[i] = local[i]
		case local[i] == original:
			merged[i] = remote[i]
		case remote[i] == original:
			merged[i] = local[i]
		default:
			line, ok := mergeNonOverlappingText(original, local[i], remote[i])
			if !ok {
				return nil, false
			}
			merged[i] = line
		}
	}
	return merged, true
}

type textEdit struct {
	start, end int
	text       []rune
}

// mergeNonOverlappingText handles independent edits inside one paragraph. The
// old merger treated any two changes on the same line as a conflict, even when
// one side changed the first word and the other changed the last.
func mergeNonOverlappingText(base, local, remote string) (string, bool) {
	a, b, c := []rune(base), []rune(local), []rune(remote)
	localEdits, ok := diffRunes(a, b)
	if !ok {
		return "", false
	}
	remoteEdits, ok := diffRunes(a, c)
	if !ok {
		return "", false
	}
	all := append([]textEdit{}, localEdits...)
	for _, remoteEdit := range remoteEdits {
		duplicate := false
		for localIndex, localEdit := range localEdits {
			if localEdit.start == remoteEdit.start && localEdit.end == remoteEdit.end && string(localEdit.text) == string(remoteEdit.text) {
				duplicate = true
				break
			}
			if localEdit.start == localEdit.end && remoteEdit.start == remoteEdit.end && localEdit.start == remoteEdit.start {
				left, right := string(localEdit.text), string(remoteEdit.text)
				switch {
				case strings.Contains(left, right):
					all[localIndex].text = []rune(left)
				case strings.Contains(right, left):
					all[localIndex].text = []rune(right)
				case left < right:
					all[localIndex].text = []rune(left + right)
				default:
					all[localIndex].text = []rune(right + left)
				}
				duplicate = true
				break
			}
			if textEditsOverlap(localEdit, remoteEdit) {
				return "", false
			}
		}
		if !duplicate {
			all = append(all, remoteEdit)
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].start != all[j].start {
			return all[i].start > all[j].start
		}
		return all[i].end > all[j].end
	})
	merged := append([]rune{}, a...)
	for _, edit := range all {
		if edit.start < 0 || edit.end < edit.start || edit.end > len(merged) {
			return "", false
		}
		next := make([]rune, 0, len(merged)-(edit.end-edit.start)+len(edit.text))
		next = append(next, merged[:edit.start]...)
		next = append(next, edit.text...)
		next = append(next, merged[edit.end:]...)
		merged = next
	}
	return string(merged), true
}

func textEditsOverlap(a, b textEdit) bool {
	if a.start == a.end && b.start == b.end {
		return a.start == b.start
	}
	if a.start == a.end {
		return b.start < a.start && a.start < b.end
	}
	if b.start == b.end {
		return a.start < b.start && b.start < a.end
	}
	return max(a.start, b.start) < min(a.end, b.end)
}

func diffRunes(base, changed []rune) ([]textEdit, bool) {
	start := 0
	for start < len(base) && start < len(changed) && base[start] == changed[start] {
		start++
	}
	baseEnd, changedEnd := len(base), len(changed)
	for baseEnd > start && changedEnd > start && base[baseEnd-1] == changed[changedEnd-1] {
		baseEnd--
		changedEnd--
	}
	base, changed = base[start:baseEnd], changed[start:changedEnd]
	if len(base)*len(changed) > 1_000_000 {
		return nil, false
	}
	width := len(changed) + 1
	lcs := make([]int, (len(base)+1)*width)
	for i := len(base) - 1; i >= 0; i-- {
		for j := len(changed) - 1; j >= 0; j-- {
			if base[i] == changed[j] {
				lcs[i*width+j] = 1 + lcs[(i+1)*width+j+1]
			} else {
				lcs[i*width+j] = max(lcs[(i+1)*width+j], lcs[i*width+j+1])
			}
		}
	}
	var edits []textEdit
	editStart, editEnd := start, start
	var added []rune
	flush := func() {
		if editStart != editEnd || len(added) != 0 {
			edits = append(edits, textEdit{start: editStart, end: editEnd, text: append([]rune{}, added...)})
		}
		added = nil
	}
	for i, j := 0, 0; i < len(base) || j < len(changed); {
		if i < len(base) && j < len(changed) && base[i] == changed[j] {
			flush()
			i++
			j++
			editStart, editEnd = start+i, start+i
		} else if j < len(changed) && (i == len(base) || lcs[i*width+j+1] >= lcs[(i+1)*width+j]) {
			added = append(added, changed[j])
			j++
		} else {
			i++
			editEnd = start + i
		}
	}
	flush()
	return edits, true
}

type lineSequenceEdit struct {
	start, end int
	lines      []string
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func lineSequenceEditsOverlap(a, b lineSequenceEdit) bool {
	if a.start == a.end && b.start == b.end {
		return a.start == b.start
	}
	if a.start == a.end {
		return b.start < a.start && a.start < b.end
	}
	if b.start == b.end {
		return a.start < b.start && b.start < a.end
	}
	return max(a.start, b.start) < min(a.end, b.end)
}

func diffLineSequence(base, changed []string) ([]lineSequenceEdit, error) {
	if len(base)*len(changed) > 1_000_000 {
		return nil, fmt.Errorf("%w: too many changed lines; merge smaller edits", ErrUnsafeUpdate)
	}
	width := len(changed) + 1
	lcs := make([]int, (len(base)+1)*width)
	for i := len(base) - 1; i >= 0; i-- {
		for j := len(changed) - 1; j >= 0; j-- {
			if base[i] == changed[j] {
				lcs[i*width+j] = 1 + lcs[(i+1)*width+j+1]
			} else {
				lcs[i*width+j] = max(lcs[(i+1)*width+j], lcs[i*width+j+1])
			}
		}
	}
	var edits []lineSequenceEdit
	start, end := 0, 0
	var added []string
	flush := func() {
		if start != end || len(added) != 0 {
			lines := append([]string{}, added...)
			leading := start == 0 && len(lines) > 1 && lines[0] == "" && (len(base) == 0 || base[0] != "")
			if leading {
				edits = append(edits, lineSequenceEdit{start: start, end: start, lines: []string{""}})
				lines = lines[1:]
			}
			// strings.Split represents a terminal newline as one final empty line.
			// Keep that insertion separate from a replacement of the preceding
			// final line so a lost-response retry can recognize it independently.
			trailing := end == len(base) && len(lines) > 1 && lines[len(lines)-1] == "" && (len(base) == 0 || base[len(base)-1] != "")
			if trailing {
				lines = lines[:len(lines)-1]
			}
			if start != end || len(lines) != 0 {
				edits = append(edits, lineSequenceEdit{start: start, end: end, lines: lines})
			}
			if trailing {
				edits = append(edits, lineSequenceEdit{start: end, end: end, lines: []string{""}})
			}
		}
		added = nil
	}
	for i, j := 0, 0; i < len(base) || j < len(changed); {
		if i < len(base) && j < len(changed) && base[i] == changed[j] {
			flush()
			i++
			j++
			start, end = i, i
		} else if j < len(changed) && (i == len(base) || lcs[i*width+j+1] >= lcs[(i+1)*width+j]) {
			added = append(added, changed[j])
			j++
		} else {
			i++
			end = i
		}
	}
	flush()
	return edits, nil
}

func sameSavedText(a, b string) bool {
	return sameNotionText(canonicalTables(stableNotionMarkdown(a)), canonicalTables(stableNotionMarkdown(b)))
}

func partialEmptyPageWrite(base, remote, text string) bool {
	trimmed := strings.TrimSpace(base)
	if trimmed != "<empty-block/>" && trimmed != "<empty-block />" {
		return false
	}
	return len(remote) < len(text) && strings.HasPrefix(text, remote) && text[len(remote)] == '\n'
}
