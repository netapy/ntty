package main

import (
	"fmt"
	"github.com/gdamore/tcell/v2"
	"html"
	"ntty/internal/notion"
	"ntty/internal/store"
	"os"
	"strings"
	"testing"
	"time"
)

// Opt-in captures render the real widgets with entirely fictional content.
func TestReadmeScreenshots(t *testing.T) {
	if os.Getenv("NTTY_SCREENSHOTS") != "1" {
		t.Skip("set NTTY_SCREENSHOTS=1")
	}
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := notion.Page{ID: "demo-studio", Title: "Lunar Studio", Kind: "page", ParentKind: "workspace"}
	pages := []notion.Page{root}
	for i, title := range []string{"Mission control", "Product roadmap", "Design notes", "Engineering", "Team handbook"} {
		pages = append(pages, notion.Page{ID: fmt.Sprintf("demo-%d", i), Title: title, Kind: "page", ParentID: root.ID, ParentKind: "page_id"})
	}
	a := newApp(notion.NewDemo(nil), s, store.State{Pages: pages, Recents: pages[1:4], Pins: pages[1:2]}, nil, "", true)
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	a.ui.SetScreen(screen)
	screen.SetSize(116, 32)
	done := make(chan error, 1)
	go func() { done <- a.ui.Run() }()
	defer func() { a.ui.QueueUpdate(func() { a.cancel(); a.ui.Stop() }); <-done }()
	content := "# A little space to think.\n\nOne workspace. Your keyboard. A calmer way to build.\n\n## This week\n- [x] Ship the new onboarding flow\n- [ ] Polish the command palette\n- [ ] Make room for the next good idea\n\n## Field notes\nGreat tools get out of the way. **Keep it simple.**\n\n- Research lives next to the roadmap\n- Decisions stay close to the work\n- Small details make the whole thing feel right\n\n> Less switching. More making.\n\n---\n\nNext up: launch notes and a well-earned coffee."
	a.ui.QueueUpdateDraw(func() {
		a.active = pages[1].ID
		a.listed = pages
		a.treeExpanded[root.ID] = true
		d := &notion.Doc{Page: pages[1], Text: content, Base: notion.Content{Object: "page_markdown", Markdown: content}, Fetched: time.Now()}
		a.docs[d.Page.ID] = d
		a.showDoc(d)
		a.rebuildList()
		a.ui.SetFocus(a.editor)
	})
	if err := os.MkdirAll("docs/images", 0755); err != nil {
		t.Fatal(err)
	}
	capture := func(name string) {
		a.ui.QueueUpdate(func() {
			var svg strings.Builder
			svg.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" width="1440" height="888" viewBox="0 0 1440 888"><rect width="1440" height="888" rx="24" fill="#191a21"/><rect x="24" y="24" width="1392" height="840" rx="14" fill="#282a36"/><path d="M24 78H1416" stroke="#44475a"/><circle cx="49" cy="51" r="6" fill="#ff5555"/><circle cx="71" cy="51" r="6" fill="#f1fa8c"/><circle cx="93" cy="51" r="6" fill="#50fa7b"/><text x="720" y="57" text-anchor="middle" fill="#6272a4" font-family="monospace" font-size="14">ntty — Lunar Studio</text><g font-family="'DejaVu Sans Mono', monospace" font-size="17">`)
			for y := 0; y < 32; y++ {
				for x := 0; x < 116; x++ {
					r, comb, style, _ := screen.GetContent(x, y)
					if r == 0 || r == ' ' {
						continue
					}
					fg, _, attrs := style.Decompose()
					color := "#f8f8f2"
					if attrs&tcell.AttrDim != 0 {
						color = "#8993b5"
					}
					if fg == tcell.ColorTeal {
						color = "#8be9fd"
					}
					if fg == tcell.ColorOlive {
						color = "#f1fa8c"
					}
					weight := "normal"
					if attrs&tcell.AttrBold != 0 {
						weight = "bold"
						if fg == tcell.ColorDefault {
							color = "#bd93f9"
						}
					}
					fmt.Fprintf(&svg, `<text x="%d" y="%d" fill="%s" font-weight="%s">%s</text>`, 44+x*11, 111+y*23, color, weight, html.EscapeString(string(r)+string(comb)))
				}
			}
			svg.WriteString(`</g></svg>`)
			if err := os.WriteFile("docs/images/"+name+".svg", []byte(svg.String()), 0644); err != nil {
				t.Error(err)
			}
		})
	}
	capture("workspace")
	a.ui.QueueUpdateDraw(func() { a.state.SidebarHidden = true; a.openPalette(">") })
	capture("commands")
}
