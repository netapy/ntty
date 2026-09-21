package notion

import (
	"regexp"
	"strings"
)

var draftTable = regexp.MustCompile(`(?ms)^<table\b[^>]*>.*?</table>`)
var draftRow = regexp.MustCompile(`(?s)<tr>.*?</tr>`)
var draftCell = regexp.MustCompile(`(?s)<td\b[^>]*>.*?</td>`)

// IsDuplicatedTableAttempt identifies the old retry bug's payload: its only
// change is inserting another copy of a table already in the preflight base.
// The originating draft must itself contain just one copy of that table.
func IsDuplicatedTableAttempt(p *PendingSave) bool {
	if p == nil {
		return false
	}
	base, text, draft := canonicalTables(p.Base.Markdown), canonicalTables(p.Text), canonicalTables(p.Draft)
	for _, loc := range draftTable.FindAllStringIndex(text, -1) {
		table := text[loc[0]:loc[1]]
		if strings.Count(base, table) != 1 || strings.Count(draft, table) != 1 || strings.Count(text, table) != 2 {
			continue
		}
		end := loc[1]
		if end < len(text) && text[end] == '\n' {
			end++
		}
		if text[:loc[0]]+text[end:] == base {
			return true
		}
	}
	return false
}

// RepairNewDraftTables recovers the old editor's malformed, newly inserted
// tables. Keep every cell; pad short rows and expose stray text below the table.
// Existing remote tables and unknown markup are never rewritten here.
func RepairNewDraftTables(base, text string) string {
	repair := func(table string) string {
		if strings.Contains(base, table) {
			return table
		}
		clean := blankTableColumns.ReplaceAllString(table, "")
		rows := draftRow.FindAllString(clean, -1)
		if len(rows) == 0 {
			return table
		}
		cells := make([][]string, len(rows))
		width := 0
		var loose []string
		bad := false
		for i, row := range rows {
			cells[i] = draftCell.FindAllString(row, -1)
			if len(cells[i]) == 0 {
				return table
			}
			width = max(width, len(cells[i]))
			residue := draftCell.ReplaceAllString(row, "")
			residue = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(residue, "<tr>"), "</tr>"))
			if residue != "" {
				bad = true
				loose = append(loose, residue)
			}
		}
		openEnd := strings.Index(clean, ">") + 1
		residue := strings.TrimSpace(draftRow.ReplaceAllString(clean[openEnd:len(clean)-len("</table>")], ""))
		if residue != "" {
			bad = true
			loose = append(loose, residue)
		}
		for _, row := range cells {
			if len(row) != width {
				bad = true
			}
		}
		if !bad || strings.ContainsAny(strings.Join(loose, "\n"), "<>") {
			return table
		}
		var out strings.Builder
		out.WriteString(clean[:openEnd])
		for _, row := range cells {
			out.WriteString("\n<tr>\n")
			out.WriteString(strings.Join(row, "\n"))
			for n := len(row); n < width; n++ {
				out.WriteString("\n<td></td>")
			}
			out.WriteString("\n</tr>")
		}
		out.WriteString("\n</table>")
		if len(loose) > 0 {
			out.WriteString("\n" + strings.Join(loose, "\n"))
		}
		return out.String()
	}
	var out strings.Builder
	for i := 0; i < len(text); {
		if text[i] == '`' || text[i] == '~' {
			if end := markdownLiteralEnd(text, i); end > i {
				out.WriteString(text[i:end])
				i = end
				continue
			}
		}
		if strings.HasPrefix(text[i:], "<table") && (i == 0 || text[i-1] == '\n') {
			if loc := draftTable.FindStringIndex(text[i:]); loc != nil && loc[0] == 0 {
				out.WriteString(repair(text[i : i+loc[1]]))
				i += loc[1]
				continue
			}
		}
		out.WriteByte(text[i])
		i++
	}
	return out.String()
}
