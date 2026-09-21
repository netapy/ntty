package main

import (
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"ntty/internal/notion"
	"ntty/internal/store"
)

const (
	captureWidth, captureHeight = 116, 32
	cellWidth, cellHeight       = 10.5, 22.0
	textLeft, textTop           = 40.0, 103.0
	draculaText                 = "#f8f8f2"
)

// sandbox is the fictional workspace behind every published capture: a small
// studio with a handful of pages. No real IDs, people, or content.
func sandbox() ([]notion.Page, map[string]string) {
	pages := []notion.Page{
		{ID: "5b7c1a2e-0d3f-4a91-9c48-2f6e1d0a7b34", Title: "Studio", Kind: "page", ParentKind: "workspace"},
		{ID: "1d9f3b62-7c05-4e2a-8f11-6a3c5d9e0b47", Title: "Roadmap", Kind: "page"},
		{ID: "7a2e4c31-9b18-4d55-a0f6-3e8c2b7d1a90", Title: "Launch checklist", Kind: "page"},
		{ID: "c4f0a917-2e63-4b8d-9a27-5d1f8c3e6b02", Title: "Design system", Kind: "page"},
		{ID: "2e6b8d04-5a71-4c39-b8e2-0f9d4a7c3e15", Title: "Standup notes", Kind: "page"},
		{ID: "9c3d7f58-1a42-4e6b-8d90-7b2e5f0a1c83", Title: "Reading list", Kind: "page"},
		{ID: "6f1b5a29-8d74-4c03-9e15-2a8c4b6d0f97", Title: "Archive", Kind: "page"},
	}
	for i := 1; i < len(pages); i++ {
		pages[i].ParentID, pages[i].ParentKind = pages[0].ID, "page_id"
	}
	design := strings.ReplaceAll(pages[3].ID, "-", "")
	texts := map[string]string{
		pages[0].ID: "# Studio\n\nEverything else hangs off this page.\n",
		pages[1].ID: "# Roadmap\n\n## Now\n\n- One editable document per page\n- Safe two-way sync\n- Local drafts that survive a crash\n\n## Next\n\n- Comments and revisions in the sidebar\n- Faster startup on large workspaces\n",
		pages[2].ID: "# Launch checklist\n\nShip the 0.1 beta to a small group this week.\n\n## Blocking\n\n- [x] Freeze the command surface\n- [x] Write the release notes\n- [ ] Sign the macOS build\n- [ ] Publish the installer\n\n## Rollout\n\n<table header-row=\"true\">\n\t<tr>\n\t\t<td>Group</td>\n\t\t<td>People</td>\n\t\t<td>Date</td>\n\t</tr>\n\t<tr>\n\t\t<td>Internal</td>\n\t\t<td>12</td>\n\t\t<td>Mon</td>\n\t</tr>\n\t<tr>\n\t\t<td>Beta</td>\n\t\t<td>250</td>\n\t\t<td>Thu</td>\n\t</tr>\n</table>\n\n## Notes\n\nBeta invites go out <mention-date start=\"2026-09-24\"/>.\n\n> Keep the first run quiet: open the last page, sync in the background.\n\nSee <mention-page url=\"https://www.notion.so/" + design + "\">Design system</mention-page> for the icon set.\n",
		pages[3].ID: "# Design system\n\n## Principles\n\n1. Quiet by default\n2. One accent color\n3. Nothing moves unless it helps\n\n## Icons\n\n- 16px grid, 1.5px stroke\n- Current color only\n\n> Prefer words to icons in the sidebar.\n",
		pages[4].ID: "# Standup notes\n\n## Yesterday\n\n- Drafted the installer\n\n## Today\n\n- Review the sync preflight\n- Triage the beta feedback\n",
		pages[5].ID: "# Reading list\n\n- Designing Data-Intensive Applications\n- The Pragmatic Programmer\n- Crafting Interpreters\n",
		pages[6].ID: "# Archive\n\nOld plans. Nothing to do here.\n",
	}
	return pages, texts
}

type capture struct {
	app    *app
	screen tcell.SimulationScreen
	done   chan error
}

// startCapture runs the real widgets on a simulation screen with a cached,
// clean copy of the sandbox workspace.
func startCapture(t *testing.T, pages []notion.Page, texts map[string]string, state store.State, active string) *capture {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cached := make([]notion.Doc, 0, len(pages))
	for _, p := range pages {
		cached = append(cached, notion.Doc{Page: p, Base: notion.Content{Object: "page_markdown", ID: p.ID, Markdown: texts[p.ID]}, Text: texts[p.ID], Fetched: time.Now()})
	}
	a := newApp(notion.NewDemo(cached), s, state, cached, "", true)
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	a.ui.SetScreen(screen)
	screen.SetSize(captureWidth, captureHeight)
	c := &capture{app: a, screen: screen, done: make(chan error, 1)}
	go func() { c.done <- a.ui.Run() }()
	t.Cleanup(c.stop)
	c.update(func() {
		a.listed = pages
		a.treeExpanded[pages[0].ID] = true
		if d := a.docs[active]; d != nil {
			a.active = active
			a.setTitle(d.Page.Title)
			a.setBreadcrumb(d.Page)
			a.showDoc(d)
			a.editor.Select(len(a.editor.visible), len(a.editor.visible))
		}
		a.rebuildList()
		a.ui.SetFocus(a.editor)
	})
	return c
}

// update runs one scene change on the UI goroutine and redraws before it
// returns, so the next write sees the finished frame.
func (c *capture) update(fn func()) { c.app.ui.QueueUpdateDraw(fn) }

func (c *capture) stop() {
	if c.done == nil {
		return
	}
	c.app.ui.QueueUpdate(func() {
		c.app.cancel()
		c.app.ui.Stop()
	})
	<-c.done
	c.done = nil
}

// write paints the current frame as SVG: a minimal macOS window with real
// terminal cells, including inverse video for selections and the live cursor.
func (c *capture) write(path string, cursor bool) {
	c.app.ui.QueueUpdate(func() {
		cursorX, cursorY, cursorVisible := c.screen.GetCursor()
		cursorVisible = cursorVisible && cursor
		var svg strings.Builder
		svg.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" width="1302" height="888" viewBox="0 0 1302 888"><rect width="1302" height="888" rx="26" fill="#191a21"/><rect x="24" y="24" width="1254" height="840" rx="12" fill="#282a36" stroke="#44475a" stroke-width="1"/><path d="M24 70H1278" stroke="#44475a"/><circle cx="52" cy="47" r="6" fill="#ff5f57"/><circle cx="74" cy="47" r="6" fill="#febc2e"/><circle cx="96" cy="47" r="6" fill="#28c840"/><g font-family="'SF Mono', Menlo, Monaco, 'DejaVu Sans Mono', monospace" font-size="16">`)
		for y := 0; y < captureHeight; y++ {
			for x := 0; x < captureWidth; x++ {
				r, comb, style, _ := c.screen.GetContent(x, y)
				if r == 0 {
					continue
				}
				fg, bg, attrs := style.Decompose()
				text := displayColor(fg, attrs)
				block := ""
				switch {
				case attrs&tcell.AttrReverse != 0:
					block, text = text, "#282a36"
				case bg != tcell.ColorDefault && bg != tcell.ColorReset:
					block = hexColor(bg)
				}
				if cursorVisible && x == cursorX && y == cursorY {
					block, text = draculaText, "#282a36"
				}
				left, top := textLeft+float64(x)*cellWidth, textTop+float64(y)*cellHeight
				if block != "" {
					fmt.Fprintf(&svg, `<rect x="%.1f" y="%.1f" width="%.1f" height="%.0f" fill="%s"/>`, left, top-cellHeight+4, cellWidth, cellHeight, block)
				}
				if r == ' ' {
					continue
				}
				fmt.Fprintf(&svg, `<text x="%.1f" y="%.1f" fill="%s" font-weight="%s">%s</text>`, left, top, text, weight(attrs), html.EscapeString(string(r)+string(comb)))
			}
		}
		svg.WriteString(`</g></svg>`)
		if err := os.WriteFile(path, []byte(svg.String()), 0644); err != nil {
			c.app.ui.QueueUpdate(func() {})
			fmt.Fprintln(os.Stderr, "capture:", err)
		}
	})
}

func weight(attrs tcell.AttrMask) string {
	if attrs&tcell.AttrBold != 0 {
		return "bold"
	}
	return "normal"
}

func hexColor(color tcell.Color) string {
	r, g, b := color.RGB()
	return fmt.Sprintf("#%02x%02x%02x", r, g, b)
}

// displayColor keeps the published Dracula-ish palette; explicit terminal
// colors (for example a Ghostty selection) are passed through unchanged.
func displayColor(fg tcell.Color, attrs tcell.AttrMask) string {
	switch {
	case fg == tcell.ColorTeal:
		return "#8be9fd"
	case fg == tcell.ColorOlive:
		return "#f1fa8c"
	case attrs&tcell.AttrDim != 0:
		return "#8993b5"
	case attrs&tcell.AttrBold != 0:
		return "#bd93f9"
	case fg == tcell.ColorDefault || fg == tcell.ColorReset:
		return draculaText
	}
	return hexColor(fg)
}

// TestReadmeScreenshots is opt-in and writes the two images used by the README.
func TestReadmeScreenshots(t *testing.T) {
	if os.Getenv("NTTY_SCREENSHOTS") != "1" {
		t.Skip("set NTTY_SCREENSHOTS=1")
	}
	pages, texts := sandbox()
	state := store.State{Pages: pages, Pins: []notion.Page{pages[2]}, Recents: []notion.Page{pages[3], pages[4], pages[1]}}
	c := startCapture(t, pages, texts, state, pages[2].ID)
	if err := os.MkdirAll("docs/images", 0755); err != nil {
		t.Fatal(err)
	}
	c.write("docs/images/workspace.svg", true)
	c.update(func() {
		c.app.state.SidebarHidden = true
		c.app.openPalette(">")
	})
	c.write("docs/images/commands.svg", true)
}

// TestRecordDemo is opt-in and writes numbered frames for the pipeline in
// docs/media (see the release notes): NTTY_RECORD_DIR=dir go test -run RecordDemo.
func TestRecordDemo(t *testing.T) {
	dir := os.Getenv("NTTY_RECORD_DIR")
	if dir == "" {
		t.Skip("set NTTY_RECORD_DIR")
	}
	pages, texts := sandbox()
	launch, design := pages[2], pages[3]
	state := store.State{Pages: pages, Pins: []notion.Page{launch}, Recents: []notion.Page{design, pages[4], pages[1]}}
	c := startCapture(t, pages, texts, state, launch.ID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	frame := 0
	emit := func(cursor bool) {
		c.write(filepath.Join(dir, fmt.Sprintf("frame-%04d.svg", frame)), cursor)
		frame++
	}
	hold := func(n int) {
		for i := 0; i < n; i++ {
			emit((i/6)%2 == 0)
		}
	}
	hold(12)
	line := "- [ ] Invite the first beta testers"
	for i := 1; i <= len(line); i++ {
		c.update(func() {
			c.app.editor.SetText(texts[launch.ID]+line[:i], true)
			c.app.onEdit()
		})
		emit(true)
	}
	hold(8)
	c.update(func() { c.app.openPalette("") })
	emit(true)
	hold(3)
	for _, query := range []string{"d", "de", "des"} {
		c.update(func() { c.app.palette.input.SetText(query) })
		emit(true)
	}
	hold(10)
	c.update(func() {
		c.app.closeModal()
		c.app.open(design)
	})
	hold(14)
	c.update(func() { c.app.openPalette(">") })
	hold(16)
	c.update(func() { c.app.closeModal() })
	hold(6)
	c.update(func() {
		x, y, _, _ := c.app.list.GetInnerRect()
		c.app.ui.QueueEvent(tcell.NewEventMouse(x+4, y+1, tcell.ButtonNone, tcell.ModNone))
	})
	time.Sleep(60 * time.Millisecond)
	c.update(func() {})
	hold(12)
	if err := os.WriteFile(filepath.Join(dir, "fps.txt"), []byte("12\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %d frames to %s", frame, dir)
}
