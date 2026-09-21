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
	d := &Demo{pages: []Page{{ID: "demo-welcome", Title: "Welcome", Kind: "page"}, {ID: "demo-notes", Title: "Quick notes", Kind: "page"}, {ID: "demo-roadmap", Title: "This week", Kind: "page"}}, docs: map[string]Content{}}
	texts := []string{
		"# ntty demo\n\nThis page is local. Nothing here contacts Notion.\n\n## Try it\n\n- Click anywhere and start typing.\n- Drag to select text; double-click selects a word.\n- Press Escape to focus the sidebar.\n- Press Ctrl+K to find a page or run a command.\n- Press Ctrl+N to create a note.\n\n## Formatting\n\nType paragraphs, # headings, **bold text**, and lists.\n\n- [x] Check the sidebar\n- [ ] Edit this page\n\n> Drafts are written locally first, then synced.\n",
		"# Quick notes\n\n- Call the plumber\n- Renew the domain\n- Book the room for Thursday\n",
		"# This week\n\n## Focus\n\n- [ ] Finish the import script\n- [ ] Review PR #482\n\n## Friday\n\nWhat went well?\n",
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
