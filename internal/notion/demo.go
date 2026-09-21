package notion

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Demo runs through the same UI, persistence and save state machine, without ntn.
type Demo struct {
	mu       sync.Mutex
	pages    []Page
	docs     map[string]Content
	comments map[string][]Comment
}

func NewDemo(cached []Doc) *Demo {
	d := &Demo{pages: []Page{{ID: "demo-welcome", Title: "A quieter place to think", Kind: "page"}, {ID: "demo-notes", Title: "Quick notes", Kind: "page"}, {ID: "demo-roadmap", Title: "This week", Kind: "page"}}, docs: map[string]Content{}}
	texts := []string{
		"# A quieter place to think\n\nYour Notion workspace. Just the terminal.\n\n## Make yourself at home\n\n- Click anywhere and start typing.\n- Drag to select text; double-click to select a word.\n- Use Escape for pages; Shift+Tab returns to your notebook.\n- Press Ctrl+K to find a page or command.\n- Press Ctrl+N for a fresh note.\n\n## Write naturally\n\nType paragraphs, # headings, **bold text**, and lists.\n\n- [x] A little less browser\n- [ ] A little more focus\n\n> Local drafts first. Notion sync after a pause.\n\nThis is a local demo. Nothing here touches Notion.\n",
		"# Quick notes\n\nAn idea worth keeping.\n\n- Coffee with the team\n- Sketch the next thing\n",
		"# This week\n\n## Focus\n\n- [ ] Ship something useful\n- [ ] Leave room to think\n\n## Friday\n\nWhat went well?\n",
	}
	for i, p := range d.pages {
		d.docs[p.ID] = Content{Object: "page_markdown", ID: p.ID, Markdown: texts[i]}
	}
	for _, doc := range cached {
		d.docs[doc.Page.ID] = doc.Base
		found := false
		for _, p := range d.pages {
			if p.ID == doc.Page.ID {
				found = true
			}
		}
		if !found {
			d.pages = append(d.pages, doc.Page)
		}
	}
	return d
}

func (d *Demo) Search(_ context.Context, q, _ string) (Listing, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	l := Listing{}
	for _, p := range d.pages {
		if strings.Contains(strings.ToLower(p.Title), strings.ToLower(q)) {
			l.Pages = append(l.Pages, p)
		}
	}
	return l, nil
}
func (d *Demo) Query(ctx context.Context, _, cursor string) (Listing, error) {
	return d.Search(ctx, "", cursor)
}
func (d *Demo) People(context.Context, string) (PeopleListing, error) {
	return PeopleListing{People: []Person{{ID: "demo-person", Name: "Ada Lovelace"}, {ID: "demo-person-2", Name: "Grace Hopper"}}}, nil
}
func (d *Demo) Page(_ context.Context, id string) (Page, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, p := range d.pages {
		if p.ID == id {
			return p, nil
		}
	}
	return Page{}, fmt.Errorf("page not found")
}
func (d *Demo) Read(_ context.Context, id string) (Content, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	v, ok := d.docs[id]
	if !ok {
		return v, fmt.Errorf("page not found")
	}
	return v, nil
}
func (d *Demo) Save(_ context.Context, id, base, text string) (Content, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	v := d.docs[id]
	if v.Markdown != base && v.Markdown != text {
		return v, ErrConflict
	}
	v.Markdown = text
	d.docs[id] = v
	return v, nil
}
func (d *Demo) Rename(_ context.Context, page Page, title string) (Page, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i := range d.pages {
		if d.pages[i].ID == page.ID {
			d.pages[i].Title = title
			return d.pages[i], nil
		}
	}
	return page, fmt.Errorf("page not found")
}
func (d *Demo) Create(_ context.Context, _, title, text string) (Page, Content, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	p := Page{ID: fmt.Sprintf("demo-%d", time.Now().UnixNano()), Title: title, Kind: "page"}
	v := Content{Object: "page_markdown", ID: p.ID, Markdown: text}
	d.pages = append([]Page{p}, d.pages...)
	d.docs[p.ID] = v
	return p, v, nil
}
