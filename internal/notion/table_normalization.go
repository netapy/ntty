package notion

import (
	"regexp"
	"strings"
)

var blankTableColumns = regexp.MustCompile(`(?s)\n[\t ]*<colgroup>\s*(?:<col>\s*)+</colgroup>`)
var tableStructureLine = regexp.MustCompile(`^[\t ]*(?:</?(?:table|colgroup|tr|td)\b[^>]*>.*|<col(?:\s[^>]*)?>)[\t ]*$`)

// Notion omits unspecified column widths and structural table indentation.
// Only normalize complete tables outside literals; cell contents stay exact.
func canonicalTables(markdown string) string {
	var out strings.Builder
	for i := 0; i < len(markdown); {
		if markdown[i] == '`' || markdown[i] == '~' {
			if end := markdownLiteralEnd(markdown, i); end > i {
				out.WriteString(markdown[i:end])
				i = end
				continue
			}
		}
		if strings.HasPrefix(markdown[i:], "<table") && (i == 0 || markdown[i-1] == '\n') {
			if end := strings.Index(markdown[i:], "</table>"); end >= 0 {
				end += i + len("</table>")
				table := blankTableColumns.ReplaceAllString(markdown[i:end], "")
				lines := strings.Split(table, "\n")
				for n, line := range lines {
					if tableStructureLine.MatchString(line) {
						lines[n] = strings.TrimSpace(line)
					}
				}
				out.WriteString(strings.Join(lines, "\n"))
				i = end
				continue
			}
		}
		out.WriteByte(markdown[i])
		i++
	}
	return out.String()
}
