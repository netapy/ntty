package main

import (
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/mattn/go-runewidth"
	"github.com/rivo/tview"
	"ntty/internal/notion"
)

func (a *app) setBreadcrumb(page notion.Page) {
	chain := []notion.Page{page}
	seen := map[string]bool{canonicalID(page.ID): true}
	current := page
	for current.ParentKind == "page_id" && current.ParentID != "" {
		parent, ok := a.knownPage(current.ParentID)
		if !ok || seen[canonicalID(parent.ID)] {
			break
		}
		seen[canonicalID(parent.ID)] = true
		chain = append(chain, parent)
		current = parent
	}
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	if resolved := a.resolvedPaths[canonicalID(page.ID)]; len(resolved) > 0 {
		chain = resolved
	}
	a.breadcrumbPages = chain
	a.renderBreadcrumb(0)
}

type breadcrumbLink struct {
	start, end int
	page       notion.Page
}

func (a *app) renderBreadcrumb(width int) {
	chain := a.breadcrumbPages
	labels := make([]string, len(chain))
	for i, p := range chain {
		label := p.Title
		labels[i] = label
	}
	start := 0
	prefix := "  Workspace  /  "
	for width > 0 && start < len(chain)-2 && runewidth.StringWidth(prefix+strings.Join(labels[start:], "  /  ")) > width {
		start++
		prefix = "  …  /  "
	}
	if width > 0 && len(chain)-start > 0 {
		available := max(1, (width-runewidth.StringWidth(prefix)-5*(len(chain)-start-1))/(len(chain)-start))
		for i := start; i < len(labels); i++ {
			labels[i] = runewidth.Truncate(labels[i], available, "…")
		}
	}
	a.breadcrumbLinks = nil
	pos := runewidth.StringWidth(prefix)
	if start > 0 {
		a.breadcrumbLinks = append(a.breadcrumbLinks, breadcrumbLink{2, 3, chain[start-1]})
	}
	for i := start; i < len(chain); i++ {
		end := pos + runewidth.StringWidth(labels[i])
		a.breadcrumbLinks = append(a.breadcrumbLinks, breadcrumbLink{pos, end, chain[i]})
		pos = end + 5
	}
	text := tview.Escape(prefix + strings.Join(labels[start:], "  /  "))
	if a.breadcrumb.GetText(false) != text {
		a.breadcrumb.SetText(text)
	}
}

func (a *app) resolveBreadcrumb(page notion.Page) {
	if a.breadcrumbCancel != nil {
		a.breadcrumbCancel()
	}
	client, ok := a.backend.(interface {
		PagePath(context.Context, string) ([]notion.Page, error)
	})
	if !ok {
		return
	}
	ctx, cancel := context.WithCancel(a.ctx)
	a.breadcrumbCancel = cancel
	go func() {
		chain, err := client.PagePath(ctx, page.ID)
		if ctx.Err() != nil {
			return
		}
		a.ui.QueueUpdateDraw(func() {
			if ctx.Err() != nil || a.active != page.ID {
				return
			}
			if err != nil {
				return
			}
			for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
				chain[i], chain[j] = chain[j], chain[i]
			}
			for i := 1; i < len(chain); i++ {
				chain[i].ParentID = chain[i-1].ID
				chain[i].ParentKind = chain[i-1].Kind + "_id"
			}
			for _, p := range chain {
				if d := a.docs[p.ID]; d != nil {
					d.Page.ParentID = p.ParentID
					d.Page.ParentKind = p.ParentKind
				}
			}
			if a.resolvedPaths == nil {
				a.resolvedPaths = make(map[string][]notion.Page)
			}
			a.resolvedPaths[canonicalID(page.ID)] = chain
			a.state.Pages = mergePages(chain, a.state.Pages)
			a.persistState()
			a.rebuildList()
			a.setBreadcrumb(page)
		})
	}()
}

func (a *app) breadcrumbMouse(mouse tview.MouseAction, event *tcell.EventMouse) (tview.MouseAction, *tcell.EventMouse) {
	if (mouse != tview.MouseLeftClick && mouse != tview.MouseRightClick) || a.modal || !a.breadcrumb.InRect(event.Position()) {
		return mouse, event
	}
	x, _ := event.Position()
	left, _, _, _ := a.breadcrumb.GetInnerRect()
	for _, link := range a.breadcrumbLinks {
		if x >= left+link.start && x < left+link.end {
			if mouse == tview.MouseRightClick {
				a.openPageMenu(link.page)
			} else {
				a.open(link.page)
			}
			break
		}
	}
	return tview.MouseConsumed, nil
}

func (a *app) openPageMenu(page notion.Page) {
	if a.modal {
		return
	}
	list := tview.NewList().ShowSecondaryText(false).SetSelectedStyle(navigationSelection)
	add := func(label string, fn func()) { list.AddItem(label, "", 0, func() { a.closeModal(); fn() }) }
	if page.InTrash {
		add("Restore from Trash", func() { a.setPageTrash(page, false) })
		add("Close", func() {})
		list.SetBorder(true).SetTitle(" " + tview.Escape(page.Title) + " · Trash ")
		a.overlay(list, 48, 5)
		return
	}
	add("Open", func() { a.open(page) })
	add("New subpage", func() { a.open(page); a.newNote(false, true) })
	if _, ok := a.backend.(notion.RenameBackend); ok && page.Kind == "page" {
		add("Rename", func() { a.renamePageDialog(page) })
	}
	add("Favorite / unfavorite", func() { a.togglePin(page) })
	add("Reveal in sidebar", func() { a.revealPage(page) })
	if page.Kind == "page" {
		if _, ok := a.backend.(notion.TrashBackend); ok {
			add("Move to Trash", func() { a.setPageTrash(page, true) })
		}
	}
	if page.URL != "" {
		add("Copy page link", func() { a.copyText(page.URL, "Page link copied") })
		add("Open in Notion", func() { a.openExternal(page.URL) })
	}
	add("Close", func() {})
	list.SetBorder(true).SetBorderStyle(quiet).SetTitle(" " + tview.Escape(page.Title) + " ")
	a.overlay(list, 48, min(14, list.GetItemCount()+2))
}

func (a *app) copyText(text, message string) {
	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(text)
	if err := cmd.Run(); err != nil {
		a.message("Clipboard: "+err.Error(), true)
		return
	}
	a.message(message, false)
}

func (a *app) revealPage(page notion.Page) {
	a.state.SidebarHidden = false
	current := page
	for current.ParentKind == "page_id" && current.ParentID != "" {
		a.treeExpanded[canonicalID(current.ParentID)] = true
		parent, ok := a.knownPage(current.ParentID)
		if !ok {
			break
		}
		current = parent
	}
	a.persistExpandedPages()
	a.rebuildList()
	a.ui.SetFocus(a.list)
}

func (a *app) toggleSidebar() {
	a.state.SidebarHidden = !a.state.SidebarHidden
	a.persistState()
	a.sidebarWidth = -1 // Force the next layout pass to rebuild widths.
	a.ui.SetFocus(a.editor)
}

func (a *app) resizeSidebar(delta int) {
	width := a.state.SidebarWidth
	if width == 0 {
		width = 30
	}
	a.state.SidebarWidth = min(60, max(20, width+delta))
	a.state.SidebarHidden = false
	a.persistState()
	a.sidebarWidth = -1
}

func (a *app) toggleFullWidth() {
	a.state.FullWidth = !a.state.FullWidth
	a.persistState()
	a.message(map[bool]string{true: "Full width", false: "Comfortable width"}[a.state.FullWidth], false)
}

func (a *app) outline() {
	d := a.docs[a.active]
	if d == nil || a.modal {
		return
	}
	list := tview.NewList().ShowSecondaryText(false).SetSelectedStyle(navigationSelection)
	visibleOffset := 0
	for _, line := range a.editor.lines {
		display := line.display()
		if line.heading > 0 {
			pos := visibleOffset
			label := strings.Repeat("  ", max(0, line.heading-1)) + strings.TrimSpace(display)
			list.AddItem(tview.Escape(label), "", 0, func() {
				a.closeModal()
				a.editor.Select(pos, pos)
				a.editor.ensureCursor()
				a.ui.SetFocus(a.editor)
			})
		}
		visibleOffset += len(display) + 1
	}
	if list.GetItemCount() == 0 {
		list.AddItem("No headings on this page", "", 0, a.closeModal)
	}
	list.SetBorder(true).SetBorderStyle(quiet).SetTitle(" Outline · " + tview.Escape(d.Page.Title) + " ")
	a.overlay(list, 64, min(24, list.GetItemCount()+2))
}

func (a *app) blockActions() {
	if a.modal || a.editor.GetDisabled() {
		return
	}
	list := tview.NewList().ShowSecondaryText(false).SetSelectedStyle(navigationSelection)
	transform := []struct{ label, kind string }{
		{"Turn into text", "paragraph"}, {"Turn into heading 1", "heading1"},
		{"Turn into heading 2", "heading2"}, {"Turn into heading 3", "heading3"},
		{"Turn into bulleted list", "bullet"}, {"Turn into numbered list", "numbered"},
		{"Turn into to-do", "todo"}, {"Turn into quote", "quote"},
	}
	for _, item := range transform {
		kind := item.kind
		list.AddItem(item.label, "", 0, func() { a.closeModal(); a.editor.CommandBlock(kind); a.ui.SetFocus(a.editor) })
	}
	action := func(label, kind string) {
		list.AddItem(label, "", 0, func() {
			a.closeModal()
			if !a.editor.BlockAction(kind) {
				a.message("That block action is unavailable for protected Notion content", false)
			}
			a.ui.SetFocus(a.editor)
		})
	}
	action("Move block up", "move-up")
	action("Move block down", "move-down")
	action("Duplicate block", "duplicate")
	action("Delete block", "delete")
	list.AddItem("Close", "", 0, a.closeModal)
	list.SetBorder(true).SetBorderStyle(quiet).SetTitle(" Block actions ")
	a.overlay(list, 46, min(18, list.GetItemCount()+2))
}

func (a *app) insertPageLink() {
	if a.modal {
		return
	}
	pages := mergePages(a.state.Pins, mergePages(a.state.Recents, a.state.Pages))
	list := tview.NewList().ShowSecondaryText(false).SetSelectedStyle(navigationSelection)
	for _, page := range pages {
		if page.URL == "" || page.ID == a.active {
			continue
		}
		p := page
		label := p.Title
		list.AddItem(tview.Escape(label), "", 0, func() {
			a.closeModal()
			a.editor.Link(p.URL, p.Title)
			a.ui.SetFocus(a.editor)
		})
		if list.GetItemCount() >= 100 {
			break
		}
	}
	if list.GetItemCount() == 0 {
		list.AddItem("No linkable pages cached", "", 0, a.closeModal)
	}
	list.SetBorder(true).SetBorderStyle(quiet).SetTitle(" Link to page ")
	a.overlay(list, 62, min(22, list.GetItemCount()+2))
}

func (a *app) pageInfo() {
	d := a.docs[a.active]
	if d == nil {
		return
	}
	state := "Saved"
	if a.needsSync(d.Page.ID) {
		state = "Waiting to sync"
	}
	if a.savingPage == d.Page.ID {
		state = "Syncing"
	}
	if a.blocked[d.Page.ID] {
		state = "Needs review"
	}
	text := fmt.Sprintf("%s\n\n%s\n\nFetched %s", d.Page.Title, state, d.Fetched.Local().Format("2 Jan 2006 · 15:04:05"))
	v := tview.NewTextView().SetText(tview.Escape(text)).SetTextStyle(tcell.StyleDefault).SetWrap(true)
	v.SetBorder(true).SetBorderStyle(quiet).SetTitle(" Page status ")
	a.overlay(v, 56, 10)
}

type pageView struct {
	snapshot   richSnapshot
	undo, redo []richSnapshot
}

func canonicalID(id string) string {
	if len(id) == 36 && id[8] == '-' && id[13] == '-' && id[18] == '-' && id[23] == '-' && strings.Count(id, "-") == 4 {
		return strings.ToLower(id)
	}
	id = strings.ToLower(strings.ReplaceAll(id, "-", ""))
	if len(id) != 32 {
		return id
	}
	return id[:8] + "-" + id[8:12] + "-" + id[12:16] + "-" + id[16:20] + "-" + id[20:]
}
func (a *app) knownPage(id string) (notion.Page, bool) {
	id = canonicalID(id)
	if d := a.docs[id]; d != nil {
		return d.Page, true
	}
	for _, p := range a.state.Pages {
		if canonicalID(p.ID) == id {
			return p, true
		}
	}
	for _, p := range a.state.Pins {
		if canonicalID(p.ID) == id {
			return p, true
		}
	}
	return notion.Page{}, false
}
func (a *app) pageLabel(link string) string {
	if strings.HasPrefix(link, "user://") {
		id := strings.TrimPrefix(link, "user://")
		for _, person := range a.state.People {
			if person.ID == id {
				return person.Name
			}
		}
		return "Person"
	}
	id := pageID.FindString(link)
	if p, ok := a.knownPage(id); ok {
		return p.Title
	}
	return "Linked page"
}
func (a *app) followLink(link string) {
	if link == "notion:current" {
		if d := a.docs[a.active]; d != nil && d.Page.URL != "" {
			a.openExternal(d.Page.URL)
		}
		return
	}
	if strings.HasPrefix(link, "external:") {
		a.openExternal(strings.TrimPrefix(link, "external:"))
		return
	}
	if strings.HasPrefix(link, "database:") {
		a.openDatabase(canonicalID(pageID.FindString(strings.TrimPrefix(link, "database:"))))
		return
	}
	if strings.HasPrefix(link, "discussion://") {
		if a.commentState.pages == nil {
			a.commentState.pages = map[string]*pageComments{}
		}
		p := a.commentState.pages[a.active]
		if p == nil {
			p = &pageComments{drafts: map[string]string{}}
			a.commentState.pages[a.active] = p
		}
		p.target = strings.TrimPrefix(link, "discussion://")
		a.comments()
		return
	}
	u, err := url.Parse(link)
	if err != nil || u.Scheme != "https" && u.Scheme != "http" {
		a.message("Unsupported link", true)
		return
	}
	if u.Hostname() == "notion.so" || strings.HasSuffix(u.Hostname(), ".notion.so") || u.Hostname() == "notion.com" || strings.HasSuffix(u.Hostname(), ".notion.com") || strings.HasSuffix(u.Hostname(), ".notion.site") {
		id := canonicalID(pageID.FindString(u.Path))
		if id != "" {
			if p, ok := a.knownPage(id); ok {
				a.open(p)
				return
			}
			a.message("Opening linked page…", false)
			go func() {
				p, err := a.backend.Page(a.ctx, id)
				a.ui.QueueUpdateDraw(func() {
					if err != nil {
						a.message(err.Error(), true)
						return
					}
					a.state.Pages = mergePages([]notion.Page{p}, a.state.Pages)
					a.listed = mergePages([]notion.Page{p}, a.listed)
					a.rebuildList()
					a.open(p)
				})
			}()
			return
		}
	}
	a.openExternal(link)
}

func (a *app) openExternal(link string) {
	go func() {
		if err := exec.Command("open", link).Run(); err != nil && a.ctx.Err() == nil {
			a.ui.QueueUpdateDraw(func() { a.message(err.Error(), true) })
		}
	}()
}

func (a *app) openDatabase(id string) {
	if id == "" {
		a.message("Database link has no ID", true)
		return
	}
	backend, ok := a.backend.(notion.DatabaseBackend)
	if !ok {
		a.message("Database navigation is unavailable in this connection", true)
		return
	}
	a.message("Opening database…", false)
	go func() {
		sources, err := backend.DataSources(a.ctx, id)
		a.ui.QueueUpdateDraw(func() {
			if err != nil {
				a.message(err.Error(), true)
				return
			}
			if len(sources) == 0 {
				a.message("This database is available in Notion only · opening…", false)
				a.openExternal("https://www.notion.so/" + strings.ReplaceAll(id, "-", ""))
				return
			}
			if len(sources) == 1 {
				a.open(sources[0])
				return
			}
			list := tview.NewList().ShowSecondaryText(false).SetSelectedStyle(selection)
			for _, p := range sources {
				list.AddItem(tview.Escape(p.Title), "", 0, func() { a.closeModal(); a.open(p) })
			}
			list.SetBorder(true).SetTitle(" Data sources ")
			a.overlay(list, 60, min(16, len(sources)+2))
		})
	}()
}
func (a *app) navigate(back bool) {
	from, to := &a.back, &a.forward
	if !back {
		from, to = &a.forward, &a.back
	}
	if len(*from) == 0 {
		return
	}
	p := (*from)[len(*from)-1]
	*from = (*from)[:len(*from)-1]
	if d := a.docs[a.active]; d != nil {
		*to = append(*to, d.Page)
	}
	a.navigating = true
	a.open(p)
	a.navigating = false
}
func (a *app) findInPage() {
	if a.docs[a.active] == nil {
		return
	}
	input := tview.NewInputField().SetLabel("Find  ").SetFieldStyle(tcell.StyleDefault)
	input.SetChangedFunc(func(query string) { a.editor.Find(query, false) })
	input.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			if !a.editor.Find(input.GetText(), true) {
				a.message("No match", false)
			}
		}
	})
	pane := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(input, 1, 0, true).AddItem(a.textAction("Enter next · Esc close", a.closeModal), 1, 0, false)
	pane.Box = tview.NewBox()
	pane.SetBorder(true).SetBorderPadding(1, 1, 1, 1)
	a.overlay(pane, 60, 6)
}
