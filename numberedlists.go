package main

import (
	"strconv"
	"strings"
)

// Numbering belongs to a sibling list, never to the entire document. Deeper
// children don't increment their parent; moving to a new parent resets them.
func renumberLists(lines []richLine) {
	counts := map[int]int{}
	for i := range lines {
		l := &lines[i]
		indent := len(l.raw) - len(strings.TrimLeft(l.raw, " \t"))
		depth := 0
		for _, c := range l.raw[:indent] {
			if c == '\t' {
				depth += 2
			} else {
				depth++
			}
		}
		for d := range counts {
			if d > depth {
				delete(counts, d)
			}
		}
		m := numberedBlock.FindStringSubmatch(l.prefix)
		if m == nil || l.literal {
			delete(counts, depth)
			continue
		}
		counts[depth]++
		prefix := m[1] + strconv.Itoa(counts[depth]) + m[3]
		if prefix != l.prefix {
			l.raw = prefix + strings.TrimPrefix(l.raw, l.prefix)
			l.prefix, l.visual = prefix, prefix
		}
	}
}
