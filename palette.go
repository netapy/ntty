package main

import (
	"context"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"ntty/internal/notion"
)

type menuItem struct {
	label string
	run   func()
}

type commandMenu struct {
	input       *tview.InputField
	list        *tview.List
	items       []menuItem
	remoteQuery string
	remote      []notion.Page
	cancel      context.CancelFunc
	generation  int
	loading     bool
}

type blockMenu struct {
	input *tview.InputField
	list  *tview.List
	items []menuItem
	pos   int
}

var compactSearchLabel = strings.NewReplacer(" ", "", "-", "", "_", "")
var compactSearchWord = strings.NewReplacer("-", "", "_", "")

// Normalize each query once, and only compact a label when a literal match
// fails. Rebuilding string replacers for every candidate is expensive.
func searchMatcher(query string) func(string) bool {
	words := strings.Fields(strings.ToLower(query))
	compactWords := make([]string, len(words))
	for i, word := range words {
		compactWords[i] = compactSearchWord.Replace(word)
	}
	return func(label string) bool {
		if len(words) == 0 {
			return true
		}
		label = strings.ToLower(label)
		compactLabel, compacted := "", false
		for i, word := range words {
			if strings.Contains(label, word) {
				continue
			}
			if !compacted {
				compactLabel, compacted = compactSearchLabel.Replace(label), true
			}
			if !strings.Contains(compactLabel, compactWords[i]) {
				return false
			}
		}
		return true
	}
}

func matches(label, query string) bool {
	return searchMatcher(query)(label)
}

func (a *app) openPalette(initial string) {
	if a.modal {
		return
	}
	p := &commandMenu{
		input: tview.NewInputField().SetLabel("⌕  ").SetPlaceholder("Search pages or commands…"),
		list:  tview.NewList().ShowSecondaryText(false).SetHighlightFullLine(false).SetMainTextStyle(tcell.StyleDefault).SetSelectedStyle(navigationSelection),
	}
	p.input.SetFieldStyle(tcell.StyleDefault).SetPlaceholderStyle(quiet)
	a.palette = p
	activate := func() {
		i := p.list.GetCurrentItem()
		if i >= 0 && i < len(p.items) {
			p.items[i].run()
		}
	}
	p.list.SetSelectedFunc(func(int, string, string, rune) { activate() })
	p.input.SetChangedFunc(func(string) {
		if p.cancel != nil {
			p.cancel()
		}
		p.generation++
		p.loading = false
		a.filterPalette(p)
	})
	footer := a.textAction("↑↓ move   enter open   esc close", a.closeModal)
	pane := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(p.input, 2, 0, true).AddItem(p.list, 0, 1, false).AddItem(footer, 1, 0, false)
	// Flex normally leaves padding transparent; clear it with the terminal default.
	pane.Box = tview.NewBox()
	pane.SetBorder(true).SetBorderStyle(quiet).SetBorderPadding(1, 0, 1, 1)
	pane.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		switch e.Key() {
		case tcell.KeyDown, tcell.KeyUp, tcell.KeyPgDn, tcell.KeyPgUp:
			p.list.InputHandler()(e, func(tview.Primitive) {})
			return nil
		case tcell.KeyTab:
			p.list.InputHandler()(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone), func(tview.Primitive) {})
			return nil
		case tcell.KeyBacktab:
			p.list.InputHandler()(tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone), func(tview.Primitive) {})
			return nil
		case tcell.KeyEnter:
			activate()
			return nil
		}
		// Typing always goes to the query, even after scrolling with the mouse.
		if a.ui.GetFocus() != p.input {
			a.ui.SetFocus(p.input)
			p.input.InputHandler()(e, func(p tview.Primitive) { a.ui.SetFocus(p) })
			return nil
		}
		return e
	})
	a.overlay(pane, 66, 17)
	a.layers.GetPage("modal").(*tview.Flex).SetMouseCapture(func(action tview.MouseAction, e *tcell.EventMouse) (tview.MouseAction, *tcell.EventMouse) {
		if action == tview.MouseLeftDown && !pane.InRect(e.Position()) {
			a.closeModal()
			return action, nil
		}
		return action, e
	})
	p.input.SetText(initial)
	a.filterPalette(p)
	a.ui.SetFocus(p.input)
}

func (a *app) filterPalette(p *commandMenu) {
	query := strings.TrimSpace(p.input.GetText())
	commandsOnly := strings.HasPrefix(query, ">")
	query = strings.TrimSpace(strings.TrimPrefix(query, ">"))
	match := searchMatcher(query)
	p.items = nil
	p.list.Clear()
	add := func(label string, run func()) {
		p.items = append(p.items, menuItem{label, run})
		p.list.AddItem(tview.Escape(label), "", 0, nil)
	}
	finish := func(run func()) func() { return func() { a.closeModal(); run() } }
	if !commandsOnly {
		// Walk the sources in priority order without copying the workspace twice
		// on every keystroke. Deduplicate IDs and resolve trash status in O(n).
		sources := [][]notion.Page{a.state.Pins, a.state.Recents, a.state.Pages}
		if query == p.remoteQuery {
			sources = append([][]notion.Page{p.remote}, sources...)
		}
		trashed := a.trashedPages()
		seen := make(map[string]bool, len(a.state.Pages))
		count := 0
	pages:
		for _, pages := range sources {
			for _, page := range pages {
				if seen[page.ID] {
					continue
				}
				seen[page.ID] = true
				if page.InTrash || trashed[canonicalID(page.ID)] {
					continue
				}
				if !match(page.Title) {
					continue
				}
				if query == "" && count >= 5 {
					break pages
				}
				p := page
				label := p.Title
				if p.Kind == "data_source" {
					label = "▦  " + label
				}
				add(label, finish(func() { a.open(p) }))
				count++
			}
		}
	}
	commands := []menuItem{
		{"New note", func() { a.newNote(false, false) }},
		{"Toggle sidebar", a.toggleSidebar},
		{"Trash · restore deleted pages", a.openTrash},
		{"Pending writes", a.pendingWrites},
		{"Narrower sidebar", func() { a.resizeSidebar(-2) }},
		{"Wider sidebar", func() { a.resizeSidebar(2) }},
		{"Recent pages", func() { a.sidebarFilter = ""; a.loadList("", "", false, false); a.ui.SetFocus(a.sidebarFocus()) }},
		{"Refresh workspace index", func() { a.sidebarFilter = ""; a.loadList("", "", false, true); a.ui.SetFocus(a.list) }},
		{"Open page URL", a.openURL},
	}
	if a.docs[a.active] != nil {
		commands = append(commands,
			menuItem{"Back", func() { a.navigate(true) }},
			menuItem{"Forward", func() { a.navigate(false) }},
			menuItem{"Bold", func() { a.editor.Format(tcell.AttrBold) }},
			menuItem{"Italic", func() { a.editor.Format(tcell.AttrItalic) }},
			menuItem{"Underline", func() { a.editor.Format(tcell.AttrUnderline) }},
			menuItem{"Strikethrough", func() { a.editor.Format(tcell.AttrStrikeThrough) }},
			menuItem{"Inline code", a.editor.Code},
			menuItem{"New child page", func() { a.newNote(false, true) }},
			menuItem{"Rename page", func() { a.ui.SetFocus(a.title) }},
			menuItem{"Pin / unpin page", func() { a.togglePin(a.docs[a.active].Page) }},
			menuItem{"Reveal page in sidebar", func() { a.revealPage(a.docs[a.active].Page) }},
			menuItem{"Document outline", a.outline},
			menuItem{"Block actions", a.blockActions},
			menuItem{"Toggle full width", a.toggleFullWidth},
			menuItem{"Page sync status", a.pageInfo},
			menuItem{"Sync now", func() { a.save(a.active, true) }},
			menuItem{"Refresh page", a.refresh},
			menuItem{"Save a copy", func() { a.newNote(true, false) }},
			menuItem{"Compare with Notion", a.compare},
			menuItem{"Comments", a.comments},
			menuItem{"Local revisions", a.revisions})
		if a.docs[a.active].Page.URL != "" {
			commands = append(commands,
				menuItem{"Copy page link", func() { a.copyText(a.docs[a.active].Page.URL, "Page link copied") }},
				menuItem{"Open page in Notion", func() { a.openExternal(a.docs[a.active].Page.URL) }})
		}
	}
	if a.cursor != "" {
		commands = append(commands, menuItem{"Load more pages", func() { a.loadList(a.query, a.container, true, false) }})
	}
	commands = append(commands, menuItem{"Help", a.help}, menuItem{"Quit", a.quit})
	for _, command := range commands {
		if match(command.label) {
			add(command.label, finish(command.run))
		}
	}
	if !commandsOnly && query != "" {
		if p.loading {
			add("Searching Notion…", func() {})
		} else if query != p.remoteQuery {
			add("Search Notion for “"+query+"”", func() { a.searchPalette(p, query) })
		} else if a.listCache["|"+query].list.Cursor != "" {
			add("Browse all results…", finish(func() { a.sidebarFilter = query; a.loadList(query, "", false, false); a.ui.SetFocus(a.list) }))
		} else if len(p.items) == 0 {
			add("No pages found", func() {})
		}
	}
}

func (a *app) openBlockMenu() {
	if a.modal || a.editor.GetDisabled() {
		return
	}
	_, pos, _ := a.editor.GetSelection()
	b := &blockMenu{
		input: tview.NewInputField().SetLabel("/ ").SetPlaceholder("Block type…"),
		list:  tview.NewList().ShowSecondaryText(false).SetHighlightFullLine(false).SetMainTextStyle(tcell.StyleDefault).SetSelectedStyle(navigationSelection),
		pos:   pos,
	}
	b.input.SetFieldStyle(tcell.StyleDefault).SetPlaceholderStyle(quiet)
	a.slash = b
	all := []struct{ label, kind string }{
		{"Text", "paragraph"}, {"Heading 1", "heading1"}, {"Heading 2", "heading2"}, {"Heading 3", "heading3"}, {"Heading 4", "heading4"},
		{"Bulleted list", "bullet"}, {"Numbered list", "numbered"}, {"To-do list", "todo"}, {"Page", "page"}, {"Quote", "quote"},
		{"Code block", "code"}, {"Table", "table"}, {"Divider", "divider"}, {"Link to page", "page-link"},
	}
	filter := func() {
		query := b.input.GetText()
		b.items = nil
		b.list.Clear()
		for _, command := range all {
			if !matches(command.label+" "+command.kind, query) {
				continue
			}
			kind := command.kind
			b.items = append(b.items, menuItem{command.label, func() {
				a.slash = nil // A chosen block consumes the pending slash.
				a.closeModal()
				if kind == "page-link" {
					a.insertPageLink()
					return
				}
				if kind == "page" {
					a.newNote(false, true)
					return
				}
				a.editor.Select(b.pos, b.pos)
				a.editor.CommandBlock(kind)
			}})
			b.list.AddItem(command.label, "", 0, nil)
		}
	}
	activate := func() {
		i := b.list.GetCurrentItem()
		if i >= 0 && i < len(b.items) {
			b.items[i].run()
		}
	}
	b.input.SetChangedFunc(func(string) { filter() })
	b.list.SetSelectedFunc(func(int, string, string, rune) { activate() })
	pane := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(b.input, 1, 0, true).AddItem(b.list, 0, 1, false)
	pane.Box = tview.NewBox()
	pane.SetBorder(true).SetBorderStyle(quiet).SetTitle(" Insert block ")
	pane.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		switch e.Key() {
		case tcell.KeyUp, tcell.KeyDown, tcell.KeyPgUp, tcell.KeyPgDn:
			b.list.InputHandler()(e, func(tview.Primitive) {})
			return nil
		case tcell.KeyEnter:
			activate()
			return nil
		}
		return e
	})
	filter()
	_, _, screenWidth, screenHeight := a.layers.GetRect()
	if screenWidth < 38 || screenHeight < 16 {
		a.overlay(pane, 34, 13)
	} else {
		row, column := a.editor.position(b.pos)
		x, y, _, _ := a.editor.GetInnerRect()
		offset, _ := a.editor.GetOffset()
		left := min(max(0, x+column), screenWidth-34)
		top := min(max(0, y+row-offset+1), screenHeight-13)
		line := tview.NewFlex().AddItem(nil, left, 0, false).AddItem(pane, 34, 0, true).AddItem(nil, 0, 1, false)
		overlay := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(nil, top, 0, false).AddItem(line, 13, 0, true).AddItem(nil, 0, 1, false)
		dimModalBackground(overlay)
		overlay.SetMouseCapture(func(action tview.MouseAction, e *tcell.EventMouse) (tview.MouseAction, *tcell.EventMouse) {
			if action == tview.MouseLeftDown && !pane.InRect(e.Position()) {
				a.closeModal()
				return action, nil
			}
			return action, e
		})
		a.modal, a.returnFocus = true, a.ui.GetFocus()
		a.layers.AddPage("modal", overlay, true, true)
	}
	a.ui.SetFocus(b.input)
}

func (a *app) searchPalette(p *commandMenu, query string) {
	if p.loading {
		return
	}
	key := "|" + query
	if cached, ok := a.listCache[key]; ok && time.Since(cached.at) < time.Minute {
		p.remoteQuery, p.remote = query, cached.list.Pages
		a.filterPalette(p)
		return
	}
	ctx, cancel := context.WithCancel(a.ctx)
	p.cancel = cancel
	p.generation++
	generation := p.generation
	p.loading = true
	a.filterPalette(p)
	go func() {
		result, err := a.backend.Search(ctx, query, "")
		a.ui.QueueUpdateDraw(func() {
			if a.palette != p || p.generation != generation {
				return
			}
			p.loading = false
			if err != nil {
				a.message(err.Error(), true)
				a.filterPalette(p)
				return
			}
			a.listCache[key] = cachedList{result, time.Now()}
			a.state.Pages = mergePages(result.Pages, a.state.Pages)
			a.persistState()
			p.remoteQuery, p.remote = query, result.Pages
			a.filterPalette(p)
		})
	}()
}
