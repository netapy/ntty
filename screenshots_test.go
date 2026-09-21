package main

import (
	"fmt"
	"html"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"ntty/internal/notion"
	"ntty/internal/store"
)

// Opt-in captures render the real widgets with fictional demo content.
func TestReadmeScreenshots(t *testing.T) {
	if os.Getenv("NTTY_SCREENSHOTS") != "1" {
		t.Skip("set NTTY_SCREENSHOTS=1")
	}
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := notion.Page{ID: "demo-engineering", Title: "Engineering", Kind: "page", ParentKind: "workspace"}
	pages := []notion.Page{root}
	for i, title := range []string{"Migration plan", "Release checklist", "Meeting notes", "Reading list", "Archive"} {
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
	content := "# Migration plan\n\nMove the last jobs off the old scheduler.\n\n## Steps\n\n1. Export the job list\n2. Run the import against staging\n3. Compare the output with production\n\n## Checklist\n\n- [x] Export the job list\n- [ ] Test with a real export\n- [ ] Update the runbook\n\n## Batch sizes\n\n<table header-row=\"true\">\n\t<tr>\n\t\t<td>Batch</td>\n\t\t<td>Rows</td>\n\t\t<td>Time</td>\n\t</tr>\n\t<tr>\n\t\t<td>1,000</td>\n\t\t<td>1,024</td>\n\t\t<td>12s</td>\n\t</tr>\n\t<tr>\n\t\t<td>10,000</td>\n\t\t<td>10,240</td>\n\t\t<td>2m</td>\n\t</tr>\n</table>"
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
			// Minimal macOS-like window: rounded corners, a thin title bar with
			// traffic lights, no window title.
			svg.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" width="1302" height="888" viewBox="0 0 1302 888"><rect width="1302" height="888" rx="26" fill="#191a21"/><rect x="24" y="24" width="1254" height="840" rx="12" fill="#282a36" stroke="#44475a" stroke-width="1"/><path d="M24 70H1278" stroke="#44475a"/><circle cx="52" cy="47" r="6" fill="#ff5f57"/><circle cx="74" cy="47" r="6" fill="#febc2e"/><circle cx="96" cy="47" r="6" fill="#28c840"/><g font-family="'SF Mono', Menlo, Monaco, 'DejaVu Sans Mono', monospace" font-size="16">`)
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
					fmt.Fprintf(&svg, `<text x="%.1f" y="%.1f" fill="%s" font-weight="%s">%s</text>`, 40+float64(x)*10.5, 103+float64(y)*22, color, weight, html.EscapeString(string(r)+string(comb)))
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
