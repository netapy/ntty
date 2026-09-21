package notion

import (
	"net/url"
	"regexp"
	"strings"
)

var pageMentionTag = regexp.MustCompile(`^<mention-page\s+url="([^"]+)"\s*(?:/>|>[^<]*</mention-page>)`)
var mentionPageID = regexp.MustCompile(`(?i)([0-9a-f]{8}-?[0-9a-f]{4}-?[0-9a-f]{4}-?[0-9a-f]{4}-?[0-9a-f]{12})$`)

func mapPageMentions(text string, replace func(string, string) string) string {
	var out strings.Builder
	for i := 0; i < len(text); {
		if text[i] == '\\' && i+1 < len(text) {
			out.WriteString(text[i : i+2])
			i += 2
			continue
		}
		if text[i] == '`' || text[i] == '~' {
			if end := markdownLiteralEnd(text, i); end > i {
				out.WriteString(text[i:end])
				i = end
				continue
			}
		}
		if strings.HasPrefix(text[i:], "<mention-page") {
			if m := pageMentionTag.FindStringSubmatch(text[i:]); m != nil {
				u, err := url.Parse(m[1])
				if err == nil && (u.Scheme == "https" || u.Scheme == "http") && (u.Host == "www.notion.so" || u.Host == "notion.so" || u.Host == "app.notion.com" || u.Host == "www.notion.com") {
					if id := mentionPageID.FindString(u.Path); id != "" {
						out.WriteString(replace(strings.ToLower(strings.ReplaceAll(id, "-", "")), m[0]))
						i += len(m[0])
						continue
					}
				}
			}
		}
		out.WriteByte(text[i])
		i++
	}
	return out.String()
}

func canonicalPageMentions(text string) string {
	return mapPageMentions(text, func(id, raw string) string { return `<mention-page url="https://app.notion.com/p/` + id + `"/>` })
}

func alignPageMentions(base, text, remote string) (string, string) {
	spellings := map[string]string{}
	mapPageMentions(remote, func(id, raw string) string { spellings[id] = raw; return raw })
	align := func(s string) string {
		return mapPageMentions(s, func(id, raw string) string {
			if current, ok := spellings[id]; ok {
				return current
			}
			return raw
		})
	}
	return align(base), align(text)
}

// Recognize the old append/retry loop only when the originating draft adds
// one suffix and the pending payload consists exclusively of repeated copies.
// Do not infer intent from duplicate paragraphs elsewhere in a document.
func IsRepeatedInsertionAttempt(base Content, p *PendingSave) bool {
	if p == nil {
		return false
	}
	original := canonicalTables(stableNotionMarkdown(base.Markdown))
	draft := canonicalTables(stableNotionMarkdown(p.Draft))
	before := canonicalTables(stableNotionMarkdown(p.Base.Markdown))
	target := canonicalTables(stableNotionMarkdown(p.Text))
	if original == "" || !strings.HasPrefix(draft, original+"\n") {
		return false
	}
	added := strings.TrimPrefix(draft, original)
	if !strings.HasPrefix(before, original) || target != before+added {
		return false
	}
	rest := strings.TrimPrefix(before, original)
	return len(rest) >= len(added) && len(rest)%len(added) == 0 && rest == strings.Repeat(added, len(rest)/len(added))
}
