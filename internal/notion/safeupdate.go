package notion

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// ErrUnsafeUpdate leaves the local draft intact. In particular, the markdown
// API has no conditional insert into an empty page: insert_content can append
// after a concurrent write, and replace_content can erase it.
var ErrUnsafeUpdate = errors.New("cannot safely address this edit in Notion; draft retained")
var ErrProtectedContent = fmt.Errorf("%w: protected Notion content changed", ErrUnsafeUpdate)

const maxNearbyContextBytes = 8 << 10

type contentUpdate struct {
	OldStr            string `json:"old_str"`
	NewStr            string `json:"new_str"`
	ReplaceAllMatches bool   `json:"replace_all_matches"`
}

type markdownRange struct{ start, end int }
type markdownShape struct{ protected, tokens []markdownRange }
type markdownCore struct {
	start, end int
	text       string
}
type markdownEdit struct {
	start, end int
	text       string
	cores      []markdownCore
}

// PreservesProtectedObjects reports whether an editor operation retained every
// opaque Notion object and metadata token byte-for-byte and in order. Invalid
// free-form Markdown is left to the save validator so typing a literal "<" is
// not blocked mid-keystroke.
func PreservesProtectedObjects(before, after string) bool {
	a, err := inspectMarkdown(before)
	if err != nil || len(a.protected) == 0 {
		return true
	}
	b, err := inspectMarkdown(after)
	if err != nil {
		// Keep normal incremental typing possible (for example a lone "<") while
		// still refusing an edit that dropped an existing protected token.
		at := 0
		for _, x := range a.protected {
			n := strings.Index(after[at:], before[x.start:x.end])
			if n < 0 {
				return false
			}
			at += n + x.end - x.start
		}
		return true
	}
	a.protected = protectedDocumentEdges(before, after, a.protected)
	_, ok := protectedObjectMatches(before, after, a.protected, b.protected)
	return ok
}

// A block's separating newline is a barrier inside a document, but is not
// part of the object when deleting all prose before/after it. Keep the actual
// object bytes protected while allowing it to become the first/last block.
func protectedDocumentEdges(before, after string, ranges []markdownRange) []markdownRange {
	if len(ranges) == 0 {
		return ranges
	}
	ranges = append([]markdownRange(nil), ranges...)
	if len(ranges) == 1 {
		raw := before[ranges[0].start:ranges[0].end]
		if after == strings.Trim(raw, "\n") {
			if strings.HasPrefix(raw, "\n") {
				ranges[0].start++
			}
			if strings.HasSuffix(raw, "\n") {
				ranges[0].end--
			}
			return ranges
		}
	}
	first := &ranges[0]
	if before[first.start] == '\n' && strings.HasPrefix(after, before[first.start+1:first.end]) {
		first.start++
	}
	last := &ranges[len(ranges)-1]
	if last.end > last.start && before[last.end-1] == '\n' && strings.HasSuffix(after, before[last.start:last.end-1]) {
		last.end--
	}
	return ranges
}

// Existing objects must remain byte-for-byte and in order. New valid objects
// are allowed: insertion is non-destructive and Notion's
// allow_deleting_content=false remains the final server-side guard.
func protectedObjectMatches(before, after string, existing, next []markdownRange) ([]markdownRange, bool) {
	matches := make([]markdownRange, len(existing))
	at := 0
	for i, object := range existing {
		raw := before[object.start:object.end]
		for {
			relative := strings.Index(after[at:], raw)
			if relative < 0 {
				return nil, false
			}
			candidate := markdownRange{start: at + relative, end: at + relative + len(raw)}
			covered := candidate.start
			for _, protected := range next {
				if protected.end <= covered || protected.start > covered {
					continue
				}
				covered = max(covered, protected.end)
				if covered >= candidate.end {
					break
				}
			}
			if covered >= candidate.end {
				matches[i] = candidate
				at = candidate.end
				break
			}
			at = candidate.start + 1
		}
	}
	return matches, true
}

// planContentUpdates uses the documented exact-match operation, never an
// ellipsis selection or a page replacement. Protected objects are barriers,
// not search context: even an unchanged <unknown> cannot safely be re-created.
// https://developers.notion.com/reference/update-page-markdown
// https://developers.notion.com/guides/data-apis/enhanced-markdown
func planContentUpdates(base, text string) ([]contentUpdate, error) {
	a, err := inspectMarkdown(base)
	if err != nil {
		return nil, err
	}
	b, err := inspectMarkdown(text)
	if err != nil {
		return nil, err
	}
	if base == text {
		return nil, nil
	}
	if base == "" {
		return nil, fmt.Errorf("%w: empty page has no exact-match anchor; refresh or save a new page with its initial content", ErrUnsafeUpdate)
	}
	a.protected = protectedDocumentEdges(base, text, a.protected)
	matches, ok := protectedObjectMatches(base, text, a.protected, b.protected)
	if !ok {
		return nil, fmt.Errorf("%w: existing Notion object or metadata was removed or changed", ErrProtectedContent)
	}
	var edits []markdownEdit
	left, right := 0, 0
	for i := 0; i <= len(a.protected); i++ {
		endA, endB := len(base), len(text)
		if i < len(a.protected) {
			x, y := a.protected[i], matches[i]
			endA, endB = x.start, y.start
		}
		changes, err := diffMarkdownLines(base[left:endA], text[right:endB])
		if err != nil {
			// The line LCS is only an optimization. If a large edit would make
			// that matrix excessive, replace this complete editable region using
			// one exact-match target instead of refusing an otherwise safe save.
			// Protected objects still delimit the region and can never enter it.
			if endA == left {
				return nil, err
			}
			changes = []markdownEdit{{start: 0, end: endA - left, text: text[right:endB]}}
		}
		delta := 0
		for _, change := range changes {
			start, end := left+change.start, left+change.end
			newStart := right + change.start + delta
			newEnd := newStart + len(change.text)
			delta += len(change.text) - (change.end - change.start)
			old, replacement := base[start:end], change.text
			prefix, suffix := matchingAffixes(old, replacement)
			// If an edit inserts or removes block boundaries, keep its complete
			// source lines. Sending a target that starts inside inline formatting
			// and a replacement that then introduces top-level blocks can make
			// Notion import only a prefix of the replacement.
			if strings.Contains(old, "\n") || strings.Contains(replacement, "\n") {
				prefix, suffix = 0, 0
			}
			// Don't send half a UTF-8 character, tag, or styling attribute. These
			// suffix/prefix bytes are equal in old and new, so expanding retains them.
			for _, source := range []struct {
				tokens     []markdownRange
				start, end int
			}{{a.tokens, start, end}, {b.tokens, newStart, newEnd}} {
				for _, token := range source.tokens {
					if token.start < source.start+prefix && source.start+prefix < token.end {
						prefix = token.start - source.start
					}
					if token.start < source.end-suffix && source.end-suffix < token.end {
						suffix = source.end - token.end
					}
				}
			}
			if prefix < 0 || suffix < 0 {
				return nil, fmt.Errorf("%w: edit crosses a multiline token", ErrUnsafeUpdate)
			}
			start += prefix
			end -= suffix
			replacement = replacement[prefix : len(replacement)-suffix]
			lo, hi := lineContext(base, start, end, left, endA)
			from, to, ok := exactContext(base, start, end, lo, hi, a.tokens)
			if !ok && (lo != left || hi != endA) {
				// Repeated short blocks such as <empty-block/> may not uniquely anchor
				// an insertion on one line. Grow into a bounded neighborhood of nearby
				// editable blocks, never the entire segment or across protected objects.
				wideLo, wideHi := nearbyLineContext(base, start, end, left, endA, 32)
				if wideLo != left || wideHi != endA || endA-left <= maxNearbyContextBytes {
					from, to, ok = exactContext(base, start, end, wideLo, wideHi, a.tokens)
				}
			}
			if !ok {
				// Repetitive pages sometimes need more than the nearby 32-line
				// window to identify one occurrence. The whole editable region is
				// still a race-safe compare-and-swap target: any intervening change
				// makes old_str fail, and no protected object is included.
				from, to, ok = exactContext(base, start, end, left, endA, a.tokens)
			}
			if !ok {
				return nil, fmt.Errorf("%w: target near line %d is empty or ambiguous within its editable block", ErrUnsafeUpdate, 1+strings.Count(base[:start], "\n"))
			}
			core := markdownCore{start: start, end: end, text: replacement}
			contextual := base[from:start] + replacement + base[end:to]
			edits = append(edits, markdownEdit{start: from, end: to, text: contextual, cores: []markdownCore{core}})
		}
		if i < len(a.protected) {
			left, right = a.protected[i].end, matches[i].end
		}
	}
	if len(edits) == 0 {
		return nil, fmt.Errorf("%w: edit produced no exact target", ErrUnsafeUpdate)
	}
	edits = coalesceNearbyEdits(base, edits, a.protected, 0)
	if len(edits) > 100 {
		edits = coalesceEditsToLimit(base, edits, a.protected, 100)
	}
	if len(edits) > 100 {
		return nil, fmt.Errorf("%w: edit spans more than 100 independently protected regions", ErrUnsafeUpdate)
	}
	var updates []contentUpdate
	// Don't rely on an undocumented batch ordering. Each exact target must stay
	// unique after every other replacement; the simulations below verify both
	// possible endpoint orders and reproduce the complete draft exactly.
	for {
		updates = make([]contentUpdate, len(edits))
		for i, edit := range edits {
			if i > 0 && edit.start < edits[i-1].end {
				return nil, fmt.Errorf("%w: search contexts overlap", ErrUnsafeUpdate)
			}
			updates[i] = contentUpdate{OldStr: base[edit.start:edit.end], NewStr: edit.text}
		}
		badI, badJ := -1, -1
		for i, edit := range edits {
			after := base[:edit.start] + edit.text + base[edit.end:]
			for j, other := range edits {
				if i == j {
					continue
				}
				pos := other.start
				if edit.end <= pos {
					pos += len(edit.text) - (edit.end - edit.start)
				}
				if !uniqueAt(after, updates[j].OldStr, pos) {
					badI, badJ = min(i, j), max(i, j)
					break
				}
			}
			if badI >= 0 {
				break
			}
		}
		if badI < 0 {
			break
		}
		combined := coalesceNearbyEdits(base, edits[badI:badJ+1], a.protected, 512)
		if len(combined) != 1 {
			return nil, fmt.Errorf("%w: replacements would interfere with another target", ErrUnsafeUpdate)
		}
		edits = append(append(append([]markdownEdit{}, edits[:badI]...), combined[0]), edits[badJ+1:]...)
	}
	for _, reverse := range []bool{false, true} {
		result := base
		for n := range updates {
			i := n
			if reverse {
				i = len(updates) - 1 - n
			}
			u := updates[i]
			if !uniqueAt(result, u.OldStr, strings.Index(result, u.OldStr)) {
				return nil, fmt.Errorf("%w: batch targets are not independent", ErrUnsafeUpdate)
			}
			result = strings.Replace(result, u.OldStr, u.NewStr, 1)
		}
		if result != text {
			return nil, fmt.Errorf("%w: exact replacements do not reproduce the draft", ErrUnsafeUpdate)
		}
	}
	return updates, nil
}

// update_content accepts at most 100 replacements. Merge the closest safe
// targets until the request fits rather than rejecting 101 ordinary edits.
// The unchanged gap becomes part of old_str/new_str, so a concurrent edit in
// that gap makes the exact match fail instead of being overwritten.
func coalesceEditsToLimit(base string, edits []markdownEdit, protected []markdownRange, limit int) []markdownEdit {
	for len(edits) > limit {
		best, bestGap := -1, int(^uint(0)>>1)
		for i := 0; i+1 < len(edits); i++ {
			gap := edits[i+1].start - edits[i].end
			if gap < 0 || gap >= bestGap {
				continue
			}
			if merged := coalesceNearbyEdits(base, edits[i:i+2], protected, gap); len(merged) == 1 {
				best, bestGap = i, gap
			}
		}
		if best < 0 {
			break
		}
		merged := coalesceNearbyEdits(base, edits[best:best+2], protected, bestGap)
		next := make([]markdownEdit, 0, len(edits)-1)
		next = append(next, edits[:best]...)
		next = append(next, merged[0])
		next = append(next, edits[best+2:]...)
		edits = next
	}
	return edits
}

func coalesceNearbyEdits(base string, edits []markdownEdit, protected []markdownRange, maxGap int) []markdownEdit {
	if len(edits) < 2 {
		return edits
	}
	intersectsProtected := func(start, end int) bool {
		for _, object := range protected {
			if object.start < end && object.end > start {
				return true
			}
		}
		return false
	}
	build := func(start, end int, cores []markdownCore) string {
		ordered := append([]markdownCore(nil), cores...)
		sort.SliceStable(ordered, func(i, j int) bool {
			if ordered[i].start != ordered[j].start {
				return ordered[i].start > ordered[j].start
			}
			return ordered[i].end > ordered[j].end
		})
		result := base[start:end]
		for _, core := range ordered {
			from, to := core.start-start, core.end-start
			result = result[:from] + core.text + result[to:]
		}
		return result
	}
	merged := []markdownEdit{edits[0]}
	for _, edit := range edits[1:] {
		previous := &merged[len(merged)-1]
		gap := edit.start - previous.end
		betweenStart, betweenEnd := min(previous.end, edit.start), max(previous.end, edit.start)
		if gap <= maxGap && !intersectsProtected(betweenStart, betweenEnd) {
			previous.end = max(previous.end, edit.end)
			previous.cores = append(previous.cores, edit.cores...)
			previous.text = build(previous.start, previous.end, previous.cores)
			continue
		}
		merged = append(merged, edit)
	}
	return merged
}

func uniqueAt(text, needle string, at int) bool {
	return needle != "" && at >= 0 && strings.Index(text, needle) == at && strings.Index(text[at+1:], needle) < 0
}

// Grow context symmetrically, at rune/token boundaries, within touched lines.
// Uniqueness is monotonic with growing context, so binary search avoids a
// quadratic scan (and repeated string allocations) for long repeated text.
func exactContext(s string, start, end, lo, hi int, tokens []markdownRange) (int, int, bool) {
	left, right := []int{start}, []int{end}
	for i := start; i > lo; {
		_, n := utf8.DecodeLastRuneInString(s[lo:i])
		i -= n
		left = append(left, i)
	}
	for i := end; i < hi; {
		_, n := utf8.DecodeRuneInString(s[i:hi])
		i += n
		right = append(right, i)
	}
	bounds := func(n int) (int, int) {
		from, to := left[min(n, len(left)-1)], right[min(n, len(right)-1)]
		for _, token := range tokens {
			if token.start < from && from < token.end {
				from = token.start
			}
			if token.start < to && to < token.end {
				to = token.end
			}
		}
		return from, to
	}
	maxContext := max(len(left), len(right)) - 1
	from, to := bounds(maxContext)
	if from < lo || to > hi || !uniqueAt(s, s[from:to], from) {
		return 0, 0, false
	}
	n := sort.Search(maxContext+1, func(n int) bool {
		from, to := bounds(n)
		return uniqueAt(s, s[from:to], from)
	})
	from, to = bounds(n)
	return from, to, true
}

// Byte comparisons are useful for exact API matching, but their common prefix
// and suffix can end *inside* a rune (e.g. é -> ê, or € -> ₭).
func matchingAffixes(a, b string) (prefix, suffix int) {
	for prefix < min(len(a), len(b)) && a[prefix] == b[prefix] {
		prefix++
	}
	for prefix > 0 && (prefix < len(a) && !utf8.RuneStart(a[prefix]) || prefix < len(b) && !utf8.RuneStart(b[prefix])) {
		prefix--
	}
	for suffix < min(len(a), len(b))-prefix && a[len(a)-1-suffix] == b[len(b)-1-suffix] {
		suffix++
	}
	for suffix > 0 && (!utf8.RuneStart(a[len(a)-suffix]) || !utf8.RuneStart(b[len(b)-suffix])) {
		suffix--
	}
	return
}

func lineContext(s string, start, end, lo, hi int) (int, int) {
	// Appending after a final newline needs an anchor in the last existing line.
	p := start
	if p == hi && p > lo && s[p-1] == '\n' {
		p--
	}
	if i := strings.LastIndexByte(s[lo:p], '\n'); i >= 0 {
		lo += i // The separator is safe context; the preceding block is not.
	}
	if end > start && s[end-1] == '\n' {
		hi = end
	} else if i := strings.IndexByte(s[end:hi], '\n'); i >= 0 {
		hi = end + i + 1
	}
	return lo, hi
}

func nearbyLineContext(s string, start, end, lo, hi, lines int) (int, int) {
	wideLo, wideHi := start, end
	for range lines {
		if wideLo <= lo {
			wideLo = lo
			break
		}
		if i := strings.LastIndexByte(s[lo:wideLo], '\n'); i >= 0 {
			wideLo = lo + i
		} else {
			wideLo = lo
		}
	}
	for range lines {
		if wideHi >= hi {
			wideHi = hi
			break
		}
		if i := strings.IndexByte(s[wideHi:hi], '\n'); i >= 0 {
			wideHi += i + 1
		} else {
			wideHi = hi
		}
	}
	if start-wideLo > maxNearbyContextBytes {
		wideLo = start - maxNearbyContextBytes
		for wideLo < start && !utf8.RuneStart(s[wideLo]) {
			wideLo++
		}
	}
	if wideHi-end > maxNearbyContextBytes {
		wideHi = end + maxNearbyContextBytes
		for wideHi > end && wideHi < len(s) && !utf8.RuneStart(s[wideHi]) {
			wideHi--
		}
	}
	return wideLo, wideHi
}

// Line LCS keeps intervening unchanged blocks out of a replacement. Trim the
// common ends first; cap the remaining work rather than fall back to rewriting
// a large page when a draft has too many unrelated changes.
func diffMarkdownLines(a, b string) ([]markdownEdit, error) {
	x, y := strings.SplitAfter(a, "\n"), strings.SplitAfter(b, "\n")
	start := 0
	for len(x) > 0 && len(y) > 0 && x[0] == y[0] {
		start += len(x[0])
		x, y = x[1:], y[1:]
	}
	for len(x) > 0 && len(y) > 0 && x[len(x)-1] == y[len(y)-1] {
		x, y = x[:len(x)-1], y[:len(y)-1]
	}
	if len(x)*len(y) > 1_000_000 {
		return nil, fmt.Errorf("%w: too many changed lines; save smaller edits", ErrUnsafeUpdate)
	}
	width := len(y) + 1
	lcs := make([]int, (len(x)+1)*width)
	for i := len(x) - 1; i >= 0; i-- {
		for j := len(y) - 1; j >= 0; j-- {
			if x[i] == y[j] {
				lcs[i*width+j] = 1 + lcs[(i+1)*width+j+1]
			} else {
				lcs[i*width+j] = max(lcs[(i+1)*width+j], lcs[i*width+j+1])
			}
		}
	}
	var edits []markdownEdit
	var added strings.Builder
	end := start
	flush := func() {
		if start != end || added.Len() > 0 {
			edits = append(edits, markdownEdit{start: start, end: end, text: added.String()})
		}
		added.Reset()
	}
	for i, j := 0, 0; i < len(x) || j < len(y); {
		if i < len(x) && j < len(y) && x[i] == y[j] {
			flush()
			end += len(x[i])
			start = end
			i++
			j++
		} else if j < len(y) && (i == len(x) || lcs[i*width+j+1] >= lcs[(i+1)*width+j]) {
			added.WriteString(y[j])
			j++
		} else {
			end += len(x[i])
			i++
		}
	}
	flush()
	return edits, nil
}

var markdownTag = regexp.MustCompile(`^<(/?)([A-Za-z][A-Za-z0-9_-]*)((?:\s+[A-Za-z_:][A-Za-z0-9_:.-]*\s*=\s*(?:"[^"<>]*"|'[^'<>]*'))*)\s*(/?)>$`)
var markdownAttribute = regexp.MustCompile(`([A-Za-z_:][A-Za-z0-9_:.-]*)\s*=`)

// This is a preservation scanner, not a Markdown renderer. Unknown elements
// are opaque by default. Known containers expose only their text; identities,
// layout tags, mentions, media, synced content and metadata stay byte-for-byte.
func inspectMarkdown(s string) (markdownShape, error) {
	shape := markdownShape{}
	bad := func() (markdownShape, error) {
		return markdownShape{}, fmt.Errorf("%w: malformed or unsupported markdown token", ErrUnsafeUpdate)
	}
	if !utf8.ValidString(s) || strings.ContainsRune(s, 0) {
		return bad()
	}
	type element struct {
		name       string
		start, end int
		opaque     bool
		protected  bool
	}
	var stack []element
	protect := func(start, end int, block bool) {
		if block {
			line := strings.LastIndexByte(s[:start], '\n') + 1
			if strings.TrimSpace(s[line:start]) == "" {
				start = line
				if start > 0 {
					start--
				}
			}
			lineEnd := end + strings.IndexByte(s[end:]+"\n", '\n')
			if strings.TrimSpace(s[end:lineEnd]) == "" {
				end = lineEnd
				if end < len(s) {
					end++
				}
			}
		}
		shape.protected = append(shape.protected, markdownRange{start, end})
	}
	for i := 0; i < len(s); {
		if s[i] == '\\' && i+1 < len(s) {
			_, n := utf8.DecodeRuneInString(s[i+1:])
			i += 1 + n
			continue
		}
		// Only syntactically valid literals may hide tag-looking text. A run of
		// tildes in the middle of a paragraph is not a fenced code block.
		if end := markdownLiteralEnd(s, i); end != i {
			if end < 0 {
				return bad()
			}
			i = end
			continue
		}
		if strings.HasPrefix(s[i:], "![") {
			end := markdownImageEnd(s, i)
			if end < 0 {
				return bad()
			}
			protect(i, end, true)
			i = end
			continue
		}
		if s[i] == '{' && i+1 < len(s) && (s[i+1] >= 'a' && s[i+1] <= 'z') {
			end := strings.IndexByte(s[i:], '}')
			line := strings.IndexByte(s[i:], '\n')
			if end < 0 || line >= 0 && line < end {
				if strings.Contains(strings.SplitN(s[i:], "\n", 2)[0], "=") {
					return bad()
				}
			} else if strings.Contains(s[i:i+end], "=") {
				raw := s[i+1 : i+end]
				if markdownTag.FindStringSubmatch("<attrs "+raw+"/>") == nil {
					return bad()
				}
				protect(i, i+end+1, false)
				i += end + 1
				continue
			}
		}
		if s[i] != '<' {
			i++
			continue
		}
		if strings.HasPrefix(s[i:], "<!--") {
			end := strings.Index(s[i+4:], "-->")
			if end < 0 {
				return bad()
			}
			end += i + 7
			protect(i, end, true)
			i = end
			continue
		}
		end := strings.IndexByte(s[i:], '>')
		if end < 0 {
			return bad()
		}
		end += i + 1
		raw := s[i:end]
		// Standard autolinks carry no block identity.
		if (strings.HasPrefix(raw, "<https://") || strings.HasPrefix(raw, "<http://") || strings.HasPrefix(raw, "<mailto:")) && !strings.ContainsAny(raw[1:len(raw)-1], "<>\"' \t\r\n") {
			i = end
			continue
		}
		m := markdownTag.FindStringSubmatch(raw)
		if m == nil {
			return bad()
		}
		shape.tokens = append(shape.tokens, markdownRange{i, end})
		name := m[2]
		inline := name == "span" || name == "br" || strings.HasPrefix(name, "mention-")
		if m[1] == "/" {
			if m[3] != "" || m[4] != "" || len(stack) == 0 || stack[len(stack)-1].name != name {
				return bad()
			}
			open := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if open.opaque {
				protect(open.start, end, !inline)
			} else if open.protected {
				protect(i, end, !inline)
				if name == "span" && strings.TrimSpace(s[open.end:i]) == "" {
					return bad() // Don't discard a discussion's entire text anchor.
				}
			}
		} else {
			transparent := false
			switch name {
			case "callout", "details", "summary", "columns", "column", "table", "colgroup", "tr", "td", "span", "mention-user", "mention-date", "mention-page":
				transparent = true
			}
			mutable := name == "br" || name == "empty-block" || name == "span" || name == "mention-user" || name == "mention-date" || name == "mention-page"
			// Simple tables are editable document content, including whole-block
			// deletion. Opaque objects nested in their cells remain protected.
			if name == "table" || name == "tr" || name == "td" || name == "colgroup" || name == "col" {
				mutable = true
			}
			if name == "span" {
				for _, attr := range markdownAttribute.FindAllStringSubmatch(m[3], -1) {
					if attr[1] != "color" && attr[1] != "underline" {
						mutable = false
					}
				}
			}
			void := m[4] == "/" || name == "br" || name == "col"
			if void {
				if !mutable {
					protect(i, end, !inline)
				}
			} else {
				stack = append(stack, element{name, i, end, !transparent, !mutable})
				if transparent && !mutable {
					protect(i, end, !inline)
				}
			}
		}
		i = end
	}
	if len(stack) != 0 {
		return bad()
	}
	sort.Slice(shape.protected, func(i, j int) bool { return shape.protected[i].start < shape.protected[j].start })
	merged := shape.protected[:0]
	for _, p := range shape.protected {
		if len(merged) > 0 && p.start < merged[len(merged)-1].end {
			merged[len(merged)-1].end = max(merged[len(merged)-1].end, p.end)
		} else {
			merged = append(merged, p)
		}
	}
	shape.protected = merged
	return shape, nil
}

// Returns start for ordinary text, -1 for a malformed delimiter, or the byte
// after a complete code/math literal. Multiline inline literals are refused:
// we must not accidentally classify a real Notion block as example code.
func markdownLiteralEnd(s string, start int) int {
	char := s[start]
	if char != '`' && char != '~' && char != '$' {
		return start
	}
	n := 1
	for start+n < len(s) && s[start+n] == char {
		n++
	}
	if char == '~' && n < 3 {
		return start
	}
	lineStart := strings.LastIndexByte(s[:start], '\n') + 1
	lineEnd := start + strings.IndexByte(s[start:]+"\n", '\n')
	fence := strings.TrimSpace(s[lineStart:start]) == "" && (n >= 3 && char != '$' || n == 2 && char == '$' && strings.TrimSpace(s[start+n:lineEnd]) == "")
	if fence {
		for pos := lineEnd + 1; pos < len(s); {
			end := pos + strings.IndexByte(s[pos:]+"\n", '\n')
			line := strings.TrimSpace(s[pos:end])
			if len(line) >= n && strings.Trim(line, string(char)) == "" {
				return end
			}
			pos = end + 1
		}
		return -1
	}
	if char == '~' {
		return start
	}
	for pos := start + n; pos < lineEnd; {
		if s[pos] != char {
			pos++
			continue
		}
		end := pos + 1
		for end < lineEnd && s[end] == char {
			end++
		}
		if end-pos == n {
			return end
		}
		pos = end
	}
	if char == '$' {
		return start // Literal dollars in prose, not a complete inline equation.
	}
	return -1
}

func markdownImageEnd(s string, start int) int {
	depth := 1
	for i := start + 2; i < len(s); i++ {
		if s[i] == '\\' {
			i++
			continue
		}
		if s[i] == '[' {
			depth++
		} else if s[i] == ']' {
			depth--
			if depth != 0 {
				continue
			}
			if i+1 >= len(s) || s[i+1] != '(' {
				return -1
			}
			i++
			depth = 1
			for i++; i < len(s); i++ {
				if s[i] == '\\' {
					i++
					continue
				}
				if s[i] == '(' {
					depth++
				} else if s[i] == ')' {
					depth--
					if depth == 0 {
						return i + 1
					}
				}
			}
			return -1
		}
	}
	return -1
}
