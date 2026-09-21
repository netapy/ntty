package main

import (
	"encoding/xml"
	"net/url"
	"strings"
	"time"
)

// Notion objects keep their exact source; only their visible label changes.
// XML's tokenizer handles quoted attributes and entities without guessing at >.
func notionInline(text string) (raw, label, link string) {
	if strings.HasPrefix(text, "[^https://") || strings.HasPrefix(text, "[^http://") {
		if end := strings.IndexByte(text, ']'); end > 2 {
			link = text[2:end]
			return text[:end+1], "⁕", link
		}
		return "", "", ""
	}
	if strings.HasPrefix(text, "<https://") || strings.HasPrefix(text, "<http://") {
		if end := strings.IndexByte(text, '>'); end > 0 {
			link = text[1:end]
			if name := linkLabel(link); name != "" {
				return text[:end+1], name, link
			}
		}
		return "", "", ""
	}
	if !strings.HasPrefix(text, "<mention-date") && !strings.HasPrefix(text, "<mention-user") && !strings.HasPrefix(text, "<mention-page") && !strings.HasPrefix(text, "<database ") && !strings.HasPrefix(text, "<page ") {
		return
	}
	d := xml.NewDecoder(strings.NewReader(text))
	token, err := d.Token()
	if err != nil {
		return
	}
	start, ok := token.(xml.StartElement)
	if !ok {
		return
	}
	attrs := map[string]string{}
	for _, a := range start.Attr {
		attrs[a.Name.Local] = a.Value
	}
	var title strings.Builder
	for {
		token, err = d.Token()
		if err != nil {
			return "", "", ""
		}
		switch t := token.(type) {
		case xml.CharData:
			title.Write(t)
		case xml.StartElement:
			return "", "", "" // Preserve unfamiliar nested objects as-is.
		case xml.EndElement:
			if t.Name != start.Name {
				return "", "", ""
			}
			raw = text[:d.InputOffset()]
			switch start.Name.Local {
			case "mention-date":
				label = "@" + notionDate(attrs["start"])
				if attrs["end"] != "" {
					label += " – " + notionDate(attrs["end"])
				}
			case "mention-user":
				label = strings.TrimSpace(title.String())
				if label == "" {
					label = attrs["name"]
				}
				if label == "" {
					label = "Person"
				}
				label = "@" + label
			case "database", "page", "mention-page":
				label = strings.TrimSpace(title.String())
				if label == "" {
					label = attrs["title"]
				}
				if label == "" {
					label = "Untitled"
				}
				link = attrs["url"]
				if start.Name.Local == "database" {
					label = "▦ " + label
					if link != "" {
						link = "database:" + link
					}
				} else {
					label = "↗ " + label
				}
			default:
				return "", "", ""
			}
			return
		}
	}
}

func notionUserID(raw string) string {
	decoder := xml.NewDecoder(strings.NewReader(raw))
	token, err := decoder.Token()
	if err != nil {
		return ""
	}
	element, ok := token.(xml.StartElement)
	if !ok || element.Name.Local != "mention-user" {
		return ""
	}
	for _, attribute := range element.Attr {
		if attribute.Name.Local != "url" {
			continue
		}
		value := strings.TrimSuffix(strings.TrimPrefix(attribute.Value, "{{"), "}}")
		if strings.HasPrefix(value, "user://") {
			return strings.TrimPrefix(value, "user://")
		}
	}
	return ""
}

func linkLabel(link string) string {
	u, err := url.Parse(link)
	if err != nil || u.Hostname() == "" || u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	name := strings.TrimPrefix(u.Hostname(), "www.")
	path := strings.Trim(u.Path, "/")
	if path != "" {
		part := path[strings.LastIndex(path, "/")+1:]
		if decoded, err := url.PathUnescape(part); err == nil {
			part = decoded
		}
		part = strings.ReplaceAll(part, "-", " ")
		if runes := []rune(part); len(runes) > 28 {
			part = string(runes[:27]) + "…"
		}
		name += " / " + part
	}
	return name
}

func notionDate(value string) string {
	if date, err := time.Parse("2006-01-02", value); err == nil {
		return date.Format("2 Jan 2006")
	}
	if date, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return date.Format("2 Jan 2006 · 15:04 MST")
	}
	if value == "" {
		return "Date"
	}
	return value
}
