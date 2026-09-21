package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/rivo/uniseg"
	"ntty/internal/notion"
	"ntty/internal/store"
)

var quiet = tcell.StyleDefault.Dim(true)

// Use the terminal's own palette; accents never paint a background.
var accent = tcell.StyleDefault.Foreground(tcell.ColorTeal)
var section = tcell.StyleDefault.Foreground(tcell.ColorTeal).Bold(true)
var selection = tcell.StyleDefault.Bold(true).Underline(true)
var navigationSelection = tcell.StyleDefault.Bold(true).Underline(true)

type app struct {
	ui                                          *tview.Application
	layers                                      *tview.Pages
	editor                                      *richEditor
	title                                       *tview.InputField
	status                                      *tview.TextView
	logo                                        *tview.TextView
	breadcrumb                                  *breadcrumbView
	statusIcon                                  *tview.TextView
	statusExpanded, statusImportant             bool
	pageCheckBusy, trashBusy                    bool
	pageChecks                                  map[string]time.Time
	nextMetadata                                time.Time
	pageIndex                                   map[string]int
	peopleProperties                            map[string]string
	refreshDelay                                map[string]time.Duration
	backgroundUntil                             time.Time
	backgroundFailures                          int
	lastActivity                                time.Time
	demo                                        bool
	viewStates                                  map[string]pageView
	back, forward                               []notion.Page
	navigating                                  bool
	sidebarFilter                               string
	palette                                     *commandMenu
	slash                                       *blockMenu
	mention                                     *mentionMenu
	database                                    *databaseView
	commentState                                commentsState
	returnFocus                                 tview.Primitive
	noticeUntil                                 time.Time
	more                                        *tview.TextView
	sidebar                                     *tview.Flex
	favoritesLabel, recentLabel, workspaceLabel *tview.TextView
	favorites, recents, list                    *tview.List
	favoritePages, recentPages                  []notion.Page
	breadcrumbPages                             []notion.Page
	breadcrumbCancel                            context.CancelFunc
	resolvedPaths                               map[string][]notion.Page
	breadcrumbLinks                             []breadcrumbLink
	visibleDepth                                []int
	visibleHasChildren                          []bool
	treeExpanded                                map[string]bool
	sidebarWidth, sidebarHeight                 int
	root                                        *tview.Flex
	sidebarExpanded                             bool
	workspaceTruncated                          bool
	backend                                     notion.Backend
	store                                       *store.Store
	drafts                                      *draftWriter
	state                                       store.State
	docs                                        map[string]*notion.Doc
	changed                                     map[string]time.Time
	blocked                                     map[string]bool
	draftNotices                                map[string]string
	fetching                                    map[string]bool
	remoteChecked                               map[string]time.Time
	fetchCancel                                 context.CancelFunc
	fetchPage                                   string
	fetchSeq                                    int
	active                                      string
	listed, visible                             []notion.Page
	cursor, query, container, parent            string
	listing                                     bool
	startupListPending                          bool
	activityFrame                               int
	reducedMotion                               bool
	listGen                                     int
	listCancel                                  context.CancelFunc
	listCache                                   map[string]cachedList
	setting, modal, creating, quitting          bool
	renaming, titleCancel                       bool
	savingPage                                  string
	titleOriginal                               string
	ctx                                         context.Context
	cancel                                      context.CancelFunc
}

type cachedList struct {
	list notion.Listing
	at   time.Time
}

func newApp(backend notion.Backend, s *store.Store, state store.State, drafts []notion.Doc, parent string, demo bool) *app {
	ctx, cancelContext := context.WithCancel(context.Background())
	a := &app{ui: tview.NewApplication(), backend: backend, store: s, state: state, docs: map[string]*notion.Doc{}, changed: map[string]time.Time{}, blocked: map[string]bool{}, draftNotices: map[string]string{}, fetching: map[string]bool{}, remoteChecked: map[string]time.Time{}, parent: parent, ctx: ctx, listCache: map[string]cachedList{}, demo: demo, viewStates: map[string]pageView{}, treeExpanded: map[string]bool{}}
	a.reducedMotion = os.Getenv("NTTY_REDUCED_MOTION") == "1"
	a.lastActivity = time.Now()
	a.refreshDelay = map[string]time.Duration{}
	for _, id := range state.ExpandedPages {
		a.treeExpanded[canonicalID(id)] = true
	}
	a.drafts = newDraftWriter(ctx, s)
	a.cancel = func() {
		cancelContext()
		a.drafts.Wait()
	}
	for _, d := range drafts {
		doc := d
		a.docs[d.Page.ID] = &doc
		if d.Dirty || d.Pending != nil {
			a.changed[d.Page.ID] = time.Now()
		}
	}
	applyTheme()
	a.title = tview.NewInputField().SetFieldStyle(tcell.StyleDefault.Bold(true))
	a.title.SetBorderPadding(1, 0, 7, 0)
	a.title.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEscape {
			a.cancelTitle()
		} else if key == tcell.KeyEnter {
			a.commitTitle()
			a.ui.SetFocus(a.editor)
		}
	})
	a.title.SetBlurFunc(func() {
		if !a.titleCancel {
			a.commitTitle()
		}
		a.titleCancel = false
	})
	a.status = tview.NewTextView().SetDynamicColors(true)
	a.title.SetMouseCapture(func(action tview.MouseAction, e *tcell.EventMouse) (tview.MouseAction, *tcell.EventMouse) {
		if action == tview.MouseRightClick {
			if d := a.docs[a.active]; d != nil {
				a.openPageMenu(d.Page)
			}
			return tview.MouseConsumed, nil
		}
		return action, e
	})
	a.status.SetBorderPadding(0, 0, 2, 1)
	a.status.SetWrap(false).SetTextStyle(quiet)
	a.editor = newRichEditor()
	a.editor.label = a.pageLabel
	a.editor.activate = a.followLink
	a.editor.menu = a.blockActions
	a.editor.slash = a.openBlockMenu
	a.editor.mention = a.openMentionMenu
	a.editor.notice = func(text string) { a.message(text, false) }
	a.editor.SetPlaceholder("Ctrl+K to open a page or start a note.")
	a.editor.SetDisabled(true)
	a.editor.SetBorderPadding(1, 1, 2, 2)
	a.editor.SetTextStyle(tcell.StyleDefault)
	a.editor.SetSelectedStyle(selection)
	a.editor.SetPlaceholderStyle(quiet)
	// The native text engine handles Unicode typing, clipboard and paste.
	if _, err := exec.LookPath("pbcopy"); err == nil {
		a.editor.SetClipboard(func(text string) {
			cmd := exec.Command("pbcopy")
			cmd.Stdin = strings.NewReader(text)
			if err := cmd.Run(); err != nil {
				a.message("Clipboard: "+err.Error(), true)
			}
		}, func() string {
			out, err := exec.Command("pbpaste").Output()
			if err != nil {
				a.message("Clipboard: "+err.Error(), true)
			}
			return string(out)
		})
	}
	a.editor.SetChangedFunc(a.onEdit)
	a.favorites = a.newSidebarList(&a.favoritePages)
	a.recents = a.newSidebarList(&a.recentPages)
	a.list = a.newSidebarList(&a.visible)
	a.logo = tview.NewTextView()
	a.logo.SetDynamicColors(true)
	search := a.textAction("  Search  ^K", func() { a.openPalette("") })
	a.favoritesLabel = a.textAction("", func() { a.state.FavoritesClosed = !a.state.FavoritesClosed; a.persistState(); a.rebuildList() })
	a.recentLabel = a.textAction("", func() { a.state.RecentsClosed = !a.state.RecentsClosed; a.persistState(); a.rebuildList() })
	a.workspaceLabel = a.textAction("", func() { a.state.WorkspaceClosed = !a.state.WorkspaceClosed; a.persistState(); a.rebuildList() })
	for _, label := range []*tview.TextView{a.favoritesLabel, a.recentLabel, a.workspaceLabel} {
		label.SetTextStyle(section)
	}
	a.more = a.textAction("", func() {
		if !a.sidebarExpanded && a.sidebarFilter == "" && a.query == "" && a.container == "" {
			a.sidebarExpanded = true
			a.rebuildList()
			return
		}
		a.loadList(a.query, a.container, true, false)
	})
	a.sidebar = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(nil, 1, 0, false).
		AddItem(a.logo, 2, 0, false).
		AddItem(search, 2, 0, false).
		AddItem(a.favoritesLabel, 1, 0, false).
		AddItem(a.favorites, 0, 0, false).
		AddItem(a.recentLabel, 1, 0, false).
		AddItem(a.recents, 0, 0, false).
		AddItem(a.workspaceLabel, 1, 0, false).
		AddItem(a.list, 0, 1, true).
		AddItem(a.more, 1, 0, false)
	menu := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(nil, 1, 0, false).AddItem(a.button("···", func() {
		a.ui.SetFocus(a.editor)
		if d := a.docs[a.active]; d != nil {
			a.openPageMenu(d.Page)
		} else {
			a.openPalette(">")
		}
	}), 1, 0, false).AddItem(nil, 1, 0, false)
	a.breadcrumb = &breadcrumbView{TextView: tview.NewTextView().SetWrap(false), app: a}
	a.breadcrumb.SetMouseCapture(a.breadcrumbMouse)
	a.breadcrumb.SetTextStyle(accent)
	a.statusIcon = a.textAction("·", func() {
		a.statusExpanded = !a.statusExpanded
		if a.statusExpanded && a.status.GetText(false) == "" {
			a.syncStatus()
		}
	})
	a.statusIcon.SetBorderPadding(1, 0, 0, 0)
	titleRow := tview.NewFlex().AddItem(a.title, 0, 1, false).AddItem(a.statusIcon, 2, 0, false).AddItem(menu, 5, 0, false)
	pathRow := tview.NewFlex().AddItem(a.button("☰", a.toggleSidebar), 3, 0, false).AddItem(a.breadcrumb, 0, 1, false)
	header := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(pathRow, 1, 0, false).AddItem(titleRow, 2, 0, false)
	right := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(header, 3, 0, false).AddItem(a.editor, 0, 1, true).AddItem(a.status, 0, 0, false)
	divider := newSidebarDivider(a)
	initialSidebarWidth := state.SidebarWidth
	if initialSidebarWidth < 20 || initialSidebarWidth > 60 {
		initialSidebarWidth = 30
	}
	a.root = tview.NewFlex().AddItem(a.sidebar, initialSidebarWidth, 0, true).AddItem(divider, 1, 0, false).AddItem(right, 0, 1, false)
	root := a.root
	lastWidth, lastHeight, lastSideWidth := -1, -1, -1
	lastFullWidth := !state.FullWidth
	a.ui.SetBeforeDrawFunc(func(screen tcell.Screen) bool {
		right.ResizeItem(a.status, btoi(a.statusImportant || a.statusExpanded), 0)
		a.updateStatusIcon()
		width, height := screen.Size()
		sideWidth := a.state.SidebarWidth
		if sideWidth < 20 || sideWidth > max(20, width-30) {
			sideWidth = min(30, max(20, width/4))
		}
		if a.state.SidebarHidden {
			sideWidth = 0
		}
		if width != lastWidth || height != lastHeight || sideWidth != lastSideWidth || a.state.FullWidth != lastFullWidth {
			root.ResizeItem(a.sidebar, sideWidth, 0)
			root.ResizeItem(divider, btoi(!a.state.SidebarHidden), 0)
			if a.sidebarWidth != sideWidth || a.sidebarHeight != height {
				a.sidebarWidth = sideWidth
				a.sidebarHeight = height
				a.renderSidebar(sidebarSelection(a.favorites, a.favoritePages), sidebarSelection(a.recents, a.recentPages), sidebarSelection(a.list, a.visible))
			}
			divider.SetText(strings.Repeat("│\n", height))
			padding := 5 // Matches the 3-cell sidebar toggle + 2-cell breadcrumb inset.
			if a.state.FullWidth {
				padding = 2
			}
			a.editor.SetBorderPadding(1, 1, padding, 5)
			a.title.SetBorderPadding(1, 0, padding, 0)
			lastWidth, lastHeight, lastSideWidth, lastFullWidth = width, height, sideWidth, a.state.FullWidth
		}
		a.syncIndicator()
		return false
	})
	a.layers = tview.NewPages().AddPage("main", root, true, true)
	a.ui.SetRoot(a.layers, true).EnableMouse(true).EnablePaste(true).SetInputCapture(a.keys)
	installHover(a.ui, a.layers)
	mouseCapture := a.ui.GetMouseCapture()
	a.ui.SetMouseCapture(func(e *tcell.EventMouse, action tview.MouseAction) (*tcell.EventMouse, tview.MouseAction) {
		a.touchActivity()
		return mouseCapture(e, action)
	})
	a.title.SetText("Notebook")
	a.listed = state.Pages
	a.rebuildList()
	a.message("Ctrl+K to begin", false)
	return a
}

type sidebarDivider struct {
	*tview.TextView
	app      *app
	dragging bool
}

func newSidebarDivider(app *app) *sidebarDivider {
	return &sidebarDivider{TextView: tview.NewTextView().SetTextStyle(quiet), app: app}
}

func (d *sidebarDivider) MouseHandler() func(tview.MouseAction, *tcell.EventMouse, func(tview.Primitive)) (bool, tview.Primitive) {
	return d.WrapMouseHandler(func(action tview.MouseAction, event *tcell.EventMouse, _ func(tview.Primitive)) (bool, tview.Primitive) {
		x, _ := event.Position()
		resize := func() {
			rootX, _, _, _ := d.app.root.GetRect()
			d.app.state.SidebarWidth = min(60, max(20, x-rootX))
			d.app.state.SidebarHidden = false
		}
		switch action {
		case tview.MouseLeftDown:
			if !d.InRect(event.Position()) {
				return false, nil
			}
			d.dragging = true
			resize()
			return true, d
		case tview.MouseMove:
			if d.dragging {
				resize()
				return true, d
			}
		case tview.MouseLeftUp:
			if d.dragging {
				resize()
				d.dragging = false
				d.app.persistState()
				return true, nil
			}
		case tview.MouseLeftDoubleClick:
			d.app.state.SidebarWidth = 30
			d.app.state.SidebarHidden = false
			d.app.persistState()
			return true, nil
		}
		return false, nil
	})
}

func (a *app) newSidebarList(pages *[]notion.Page) *tview.List {
	list := tview.NewList().ShowSecondaryText(false).SetHighlightFullLine(false).SetSelectedFocusOnly(true).SetMainTextStyle(quiet).SetSecondaryTextStyle(quiet).SetSelectedStyle(navigationSelection)
	list.SetBorderPadding(0, 0, 1, 1)
	list.SetSelectedFunc(func(i int, _ string, _ string, _ rune) {
		if i >= 0 && i < len(*pages) {
			a.open((*pages)[i])
		}
	})
	list.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if pages == &a.visible && (e.Key() == tcell.KeyRight || e.Key() == tcell.KeyLeft) {
			i := list.GetCurrentItem()
			if i >= 0 && i < len(*pages) && i < len(a.visibleHasChildren) {
				id := canonicalID((*pages)[i].ID)
				if e.Key() == tcell.KeyRight && a.visibleHasChildren[i] {
					if !a.treeExpanded[id] {
						a.treeExpanded[id] = true
						a.persistExpandedPages()
						a.rebuildList()
					} else if i+1 < len(*pages) && a.visibleDepth[i+1] > a.visibleDepth[i] {
						list.SetCurrentItem(i + 1)
					}
					return nil
				}
				if e.Key() == tcell.KeyLeft {
					if a.visibleHasChildren[i] && a.treeExpanded[id] {
						a.treeExpanded[id] = false
						a.persistExpandedPages()
						a.rebuildList()
					} else if a.visibleDepth[i] > 0 {
						for parent := i - 1; parent >= 0; parent-- {
							if a.visibleDepth[parent] < a.visibleDepth[i] {
								list.SetCurrentItem(parent)
								break
							}
						}
					}
					return nil
				}
			}
		}
		if e.Key() == tcell.KeyUp && list.GetCurrentItem() <= 0 && a.moveSidebarFocus(list, -1) {
			return nil
		}
		if e.Key() == tcell.KeyDown && list.GetCurrentItem() >= list.GetItemCount()-1 && a.moveSidebarFocus(list, 1) {
			return nil
		}
		if e.Key() == tcell.KeyRune {
			switch e.Rune() {
			case 'p':
				a.pin()
				return nil
			case 'n':
				a.newNote(false, true)
				return nil
			case 'g':
				a.sidebarFilter = ""
				a.sidebarExpanded = false
				a.loadList("", "", false, false)
				return nil
			case 'm':
				a.loadList(a.query, a.container, true, false)
				return nil
			case '/':
				a.openPalette("")
				return nil
			case 'j':
				if list.GetCurrentItem() >= list.GetItemCount()-1 && a.moveSidebarFocus(list, 1) {
					return nil
				}
				return tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone)
			case 'k':
				if list.GetCurrentItem() <= 0 && a.moveSidebarFocus(list, -1) {
					return nil
				}
				return tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone)
			}
		}
		return e
	})
	list.SetMouseCapture(func(action tview.MouseAction, event *tcell.EventMouse) (tview.MouseAction, *tcell.EventMouse) {
		if action != tview.MouseRightClick && action != tview.MouseLeftClick {
			return action, event
		}
		i := sidebarIndexAt(list, event)
		if i < 0 || i >= len(*pages) {
			return action, event
		}
		list.SetCurrentItem(i)
		if action == tview.MouseRightClick {
			a.openPageMenu((*pages)[i])
			return tview.MouseConsumed, nil
		}
		if pages == &a.visible && i < len(a.visibleHasChildren) && a.visibleHasChildren[i] {
			x, _, _, _ := list.GetInnerRect()
			clickX, _ := event.Position()
			glyphX := x + 2 + a.visibleDepth[i]*2
			if clickX >= glyphX && clickX < glyphX+2 {
				id := canonicalID((*pages)[i].ID)
				a.treeExpanded[id] = !a.treeExpanded[id]
				a.persistExpandedPages()
				a.rebuildList()
				return tview.MouseConsumed, nil
			}
		}
		return action, event
	})
	return list
}

func sidebarIndexAt(list *tview.List, event *tcell.EventMouse) int {
	_, y, _, height := list.GetInnerRect()
	_, clickY := event.Position()
	if clickY < y || clickY >= y+height {
		return -1
	}
	offset, _ := list.GetOffset()
	return offset + clickY - y
}

func (a *app) moveSidebarFocus(from *tview.List, step int) bool {
	lists := []*tview.List{a.favorites, a.recents, a.list}
	for i, list := range lists {
		if list != from {
			continue
		}
		for next := i + step; next >= 0 && next < len(lists); next += step {
			if lists[next].GetItemCount() == 0 || !a.sidebarListVisible(lists[next]) {
				continue
			}
			if step < 0 {
				lists[next].SetCurrentItem(lists[next].GetItemCount() - 1)
			} else {
				lists[next].SetCurrentItem(0)
			}
			a.ui.SetFocus(lists[next])
			return true
		}
		break
	}
	return false
}

func (a *app) sidebarFocus() *tview.List {
	if a.recents.GetItemCount() > 0 && a.sidebarListVisible(a.recents) {
		return a.recents
	}
	if a.favorites.GetItemCount() > 0 && a.sidebarListVisible(a.favorites) {
		return a.favorites
	}
	return a.list
}

func (a *app) sidebarListVisible(list *tview.List) bool {
	showGroups := a.sidebarFilter == "" && a.query == "" && a.container == ""
	switch list {
	case a.favorites:
		return showGroups && !a.state.FavoritesClosed
	case a.recents:
		return showGroups && !a.state.RecentsClosed
	case a.list:
		return !showGroups || !a.state.WorkspaceClosed
	}
	return false
}

func (a *app) sidebarFocused() bool {
	focus := a.ui.GetFocus()
	return focus == a.favorites || focus == a.recents || focus == a.list
}

func (a *app) selectedSidebarPage() (notion.Page, bool) {
	for list, pages := range map[*tview.List][]notion.Page{a.favorites: a.favoritePages, a.recents: a.recentPages, a.list: a.visible} {
		if a.ui.GetFocus() == list {
			i := list.GetCurrentItem()
			if i >= 0 && i < len(pages) {
				return pages[i], true
			}
		}
	}
	return notion.Page{}, false
}

func (a *app) button(label string, fn func()) *tview.Button {
	b := tview.NewButton(label).SetSelectedFunc(fn)
	b.SetStyle(accent)
	b.SetActivatedStyle(navigationSelection)
	return b
}

func (a *app) textAction(label string, action func()) *tview.TextView {
	v := tview.NewTextView().SetText(label).SetTextStyle(quiet).SetWrap(false)
	v.SetMouseCapture(func(mouse tview.MouseAction, event *tcell.EventMouse) (tview.MouseAction, *tcell.EventMouse) {
		if mouse == tview.MouseLeftClick && v.InRect(event.Position()) {
			action()
			return tview.MouseConsumed, nil
		}
		return mouse, event
	})
	return v
}

func (a *app) run() error {
	defer a.cancel()
	go func() {
		a.ui.QueueUpdateDraw(func() {
			a.startupListPending = true
			if a.state.Last != "" {
				if d, err := a.store.LoadDoc(a.state.Last); err == nil {
					a.open(d.Page)
				} else if p, ok := a.knownPage(a.state.Last); ok {
					a.open(p)
				}
			}
			a.startWorkspaceRefresh()
		})
	}()
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-a.ctx.Done():
				return
			case now := <-ticker.C:
				draw := false
				a.ui.QueueUpdate(func() {
					before := a.status.GetText(false)
					wasSaving := a.syncing()
					if !a.modal {
						draw = a.editor.tickMouse()
					}
					a.autoSave(now)
					a.autoRefresh(now)
					a.startWorkspaceRefresh()
					a.refreshPageMetadata(now)
					a.checkDraftWrites()
					if before != "" && now.After(a.noticeUntil) {
						a.syncStatus()
					}
					after := a.status.GetText(false)
					draw = draw || before != after || wasSaving != a.syncing()
					if !a.reducedMotion && a.networkBusy() {
						a.activityFrame = (a.activityFrame + 1) % len(activityFrames)
						draw = true
					}
				})
				if draw {
					a.ui.Draw()
				}
			}
		}
	}()
	return a.ui.Run()
}

// Content gets the first request through the serialized Notion client.
// Workspace indexing is background work and must not delay opening a page.
func (a *app) startWorkspaceRefresh() {
	if a.startupListPending && a.fetchPage == "" && !a.syncing() && !time.Now().Before(a.backgroundUntil) {
		a.loadList("", "", false, false)
	}
}

var activityFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func (a *app) networkBusy() bool {
	return a.listing || a.fetching[a.active] || a.syncing()
}

func (a *app) message(s string, bad bool) {
	a.statusImportant = bad
	a.noticeUntil = time.Now().Add(3 * time.Second)
	if bad {
		a.status.SetTextStyle(tcell.StyleDefault.Foreground(tcell.ColorOlive))
		a.status.SetText("[::b]! " + tview.Escape(s))
		a.noticeUntil = time.Now().Add(24 * time.Hour)
	} else {
		a.status.SetTextStyle(quiet)
		a.status.SetText(tview.Escape(strings.SplitN(s, " · ", 2)[0]))
	}
}

func (a *app) syncStatus() {
	a.statusImportant = false
	a.status.SetTextStyle(quiet)
	if d := a.docs[a.active]; d != nil && a.needsSync(a.active) {
		if a.blocked[a.active] {
			a.statusImportant = true
			a.status.SetTextStyle(tcell.StyleDefault.Foreground(tcell.ColorOlive))
			a.status.SetText("[::b]! Sync needs attention · Ctrl+K → Compare with Notion")
		} else {
			a.status.SetText("Local changes · syncing automatically…")
		}
		return
	}
	if a.statusExpanded {
		if a.active != "" {
			a.status.SetText("Up to date")
		} else {
			a.status.SetText("Ctrl+K opens a page · Ctrl+\\ toggles the sidebar")
		}
	} else {
		a.status.SetText("")
	}
}

func (a *app) checkDraftWrites() {
	errs := a.drafts.Errors()
	for id, err := range errs {
		if a.draftNotices[id] != err.Error() {
			a.draftNotices[id] = err.Error()
			a.message("LOCAL DRAFT FAILED: "+err.Error()+" · do not quit until this is resolved", true)
		}
	}
	for id := range a.draftNotices {
		if errs[id] == nil {
			delete(a.draftNotices, id)
		}
	}
}

func (a *app) syncIndicator() {
	suffix := ""
	if a.demo {
		suffix = "  demo"
	}
	text := "  [teal::b]ntty[-::-]" + suffix
	if current := a.logo.GetText(false); current != text {
		a.logo.SetText(text)
	}
}

func (a *app) updateStatusIcon() {
	label, style := "·", quiet
	if a.statusImportant {
		label, style = "!", tcell.StyleDefault.Foreground(tcell.ColorOlive)
	} else if a.networkBusy() && !a.reducedMotion {
		label, style = activityFrames[a.activityFrame], accent
	} else if a.syncing() || a.needsSync(a.active) {
		label = "↑"
	} else if a.fetching[a.active] || a.listing {
		label = "↓"
	} else if a.status.GetText(false) != "" {
		label = "i"
	}
	a.statusIcon.SetText(label).SetTextStyle(style)
}

func (a *app) persistState() {
	a.indexPages()
	a.drafts.PutState(a.state)
}

func (a *app) indexPages() {
	if a.pageIndex == nil {
		a.pageIndex = make(map[string]int, len(a.state.Pages))
		a.peopleProperties = make(map[string]string)
	}
	clear(a.pageIndex)
	var people []notion.Person
	for i := len(a.state.Pages) - 1; i >= 0; i-- {
		p := a.state.Pages[i]
		a.pageIndex[canonicalID(p.ID)] = i
		if a.peopleProperties[p.ID] != p.PropertyData {
			a.peopleProperties[p.ID] = p.PropertyData
			for _, property := range p.PropertyValues() {
				people = append(people, property.People...)
			}
		}
	}
	if len(people) > 0 {
		a.state.People = mergePeople(a.state.People, people)
	}
}

func (a *app) trashedPages() map[string]bool {
	// Build once per refresh rather than scanning the entire workspace for
	// each row. Preserve knownPage's precedence: open docs, pages, then pins.
	trashed := make(map[string]bool, len(a.state.Pages))
	for _, pages := range [][]notion.Page{a.state.Pins, a.state.Pages} {
		for i := len(pages) - 1; i >= 0; i-- {
			trashed[canonicalID(pages[i].ID)] = pages[i].InTrash
		}
	}
	for id, doc := range a.docs {
		trashed[canonicalID(id)] = doc.Page.InTrash
	}
	return trashed
}

func sidebarSelection(list *tview.List, pages []notion.Page) string {
	if i := list.GetCurrentItem(); i >= 0 && i < len(pages) {
		return pages[i].ID
	}
	return ""
}

func (a *app) rebuildList() {
	a.indexPages()
	favoriteSelected := sidebarSelection(a.favorites, a.favoritePages)
	recentSelected := sidebarSelection(a.recents, a.recentPages)
	workspaceSelected := sidebarSelection(a.list, a.visible)

	trashed := a.trashedPages()
	query := strings.ToLower(a.sidebarFilter)
	filter := func(p notion.Page) bool {
		if p.InTrash || trashed[canonicalID(p.ID)] {
			return false
		}
		return query == "" || strings.Contains(strings.ToLower(p.Title), query)
	}
	a.favoritePages = nil
	for _, p := range a.state.Pins {
		if filter(p) {
			a.favoritePages = append(a.favoritePages, p)
		}
	}
	a.recentPages = nil
	seenRecent := map[string]bool{}
	for _, p := range a.state.Recents {
		if filter(p) && !seenRecent[p.ID] {
			seenRecent[p.ID] = true
			a.recentPages = append(a.recentPages, p)
		}
	}
	for _, d := range a.docs {
		if a.needsSync(d.Page.ID) && filter(d.Page) && !seenRecent[d.Page.ID] {
			seenRecent[d.Page.ID] = true
			a.recentPages = append(a.recentPages, d.Page)
		}
	}

	a.visible = nil
	a.visibleDepth = nil
	a.visibleHasChildren = nil
	seenWorkspace := map[string]bool{}
	for _, p := range a.listed {
		if seenWorkspace[p.ID] || !filter(p) {
			continue
		}
		if a.query == "" && a.container == "" && p.ParentKind != "workspace" && !a.demo {
			continue
		}
		seenWorkspace[p.ID] = true
		a.visible = append(a.visible, p)
	}
	hiddenWorkspace := !a.sidebarExpanded && a.sidebarFilter == "" && a.query == "" && a.container == "" && len(a.visible) > 20
	a.workspaceTruncated = hiddenWorkspace
	if hiddenWorkspace {
		a.visible = a.visible[:20]
	}
	normalWorkspace := a.sidebarFilter == "" && a.query == "" && a.container == "" && !a.demo
	if normalWorkspace {
		roots := append([]notion.Page{}, a.visible...)
		children := map[string][]int{}
		for i, p := range a.state.Pages {
			if p.ParentKind == "page_id" && p.ParentID != "" && filter(p) {
				id := canonicalID(p.ParentID)
				children[id] = append(children[id], i)
			}
		}
		a.visible = nil
		var appendPage func(notion.Page, int, map[string]bool)
		appendPage = func(p notion.Page, depth int, ancestors map[string]bool) {
			id := canonicalID(p.ID)
			if ancestors[id] {
				return
			}
			a.visible = append(a.visible, p)
			a.visibleDepth = append(a.visibleDepth, depth)
			a.visibleHasChildren = append(a.visibleHasChildren, len(children[id]) > 0)
			if !a.treeExpanded[id] {
				return
			}
			next := make(map[string]bool, len(ancestors)+1)
			for key, value := range ancestors {
				next[key] = value
			}
			next[id] = true
			for _, child := range children[id] {
				appendPage(a.state.Pages[child], depth+1, next)
			}
		}
		for _, root := range roots {
			appendPage(root, 0, map[string]bool{})
		}
	} else {
		for range a.visible {
			a.visibleDepth = append(a.visibleDepth, 0)
			a.visibleHasChildren = append(a.visibleHasChildren, false)
		}
	}

	a.renderSidebar(favoriteSelected, recentSelected, workspaceSelected)
}

func (a *app) renderSidebar(favoriteSelected, recentSelected, workspaceSelected string) {
	normalWorkspace := a.sidebarFilter == "" && a.query == "" && a.container == "" && !a.demo
	render := func(list *tview.List, pages []notion.Page, selected string, tree bool) {
		row, column := list.GetOffset()
		list.Clear()
		for i, p := range pages {
			marker, suffix := "  ", ""
			switch {
			case a.blocked[p.ID]:
				suffix = " !"
			case a.savingPage == p.ID:
				suffix = " ↑"
			case a.needsSync(p.ID):
				suffix = " ·"
			case p.ID == a.active:
				marker = "› "
			}
			if tree {
				marker += strings.Repeat("  ", a.visibleDepth[i])
				if a.visibleHasChildren[i] {
					if a.treeExpanded[canonicalID(p.ID)] {
						marker += "▾ "
					} else {
						marker += "▸ "
					}
				} else {
					marker += "  "
				}
			} else {
				marker += "  "
			}
			width := max(4, a.sidebarWidth-2-tview.TaggedStringWidth(marker+suffix))
			list.AddItem(tview.Escape(marker+ellipsize(p.Title, width)+suffix), "", 0, nil)
			if p.ID == selected || selected == "" && p.ID == a.active {
				list.SetCurrentItem(i)
			}
		}
		list.SetOffset(row, column)
	}
	render(a.favorites, a.favoritePages, favoriteSelected, false)
	render(a.recents, a.recentPages, recentSelected, false)
	render(a.list, a.visible, workspaceSelected, normalWorkspace)

	showGroups := a.sidebarFilter == "" && a.query == "" && a.container == ""
	favoriteHeight, recentHeight := 0, 0
	groupHeight := max(0, a.sidebarHeight-12)
	if showGroups && len(a.favoritePages) > 0 && !a.state.FavoritesClosed {
		favoriteHeight = min(min(4, len(a.favoritePages)), max(1, groupHeight/4))
	}
	if showGroups && len(a.recentPages) > 0 && !a.state.RecentsClosed {
		recentHeight = min(min(6, len(a.recentPages)), max(1, (groupHeight-favoriteHeight)/2))
	}
	a.favoritesLabel.SetText(collapsibleLabel("Favorites", a.state.FavoritesClosed))
	a.recentLabel.SetText(collapsibleLabel("Recent", a.state.RecentsClosed))
	a.sidebar.ResizeItem(a.favoritesLabel, btoi(showGroups && len(a.favoritePages) > 0), 0)
	a.sidebar.ResizeItem(a.favorites, favoriteHeight, 0)
	recentGap := btoi(favoriteHeight > 0)
	a.recentLabel.SetBorderPadding(recentGap, 0, 0, 0)
	a.sidebar.ResizeItem(a.recentLabel, (1+recentGap)*btoi(showGroups && len(a.recentPages) > 0), 0)
	a.sidebar.ResizeItem(a.recents, recentHeight, 0)
	workspaceGap := btoi(favoriteHeight+recentHeight > 0)
	a.workspaceLabel.SetBorderPadding(workspaceGap, 0, 0, 0)
	a.sidebar.ResizeItem(a.workspaceLabel, 1+workspaceGap, 0)
	if a.container != "" {
		a.workspaceLabel.SetText("  Database")
	} else if a.query != "" || a.sidebarFilter != "" {
		a.workspaceLabel.SetText("  Search results")
	} else {
		a.workspaceLabel.SetText(collapsibleLabel("Workspace", a.state.WorkspaceClosed))
	}
	a.sidebar.ResizeItem(a.list, 0, btoi(!a.state.WorkspaceClosed || !showGroups))
	a.sidebar.ResizeItem(a.more, btoi(!a.state.WorkspaceClosed || !showGroups), 0)
	if a.workspaceTruncated {
		a.more.SetText("  Show all…")
	} else if a.cursor != "" {
		a.more.SetText("  More…")
	} else {
		a.more.SetText("")
	}
}

func collapsibleLabel(label string, closed bool) string {
	if closed {
		return "  ▸ " + label
	}
	return "  ▾ " + label
}

func (a *app) persistExpandedPages() {
	a.state.ExpandedPages = a.state.ExpandedPages[:0]
	for id, expanded := range a.treeExpanded {
		if expanded {
			a.state.ExpandedPages = append(a.state.ExpandedPages, id)
		}
	}
	sort.Strings(a.state.ExpandedPages)
	a.persistState()
}

func btoi(value bool) int {
	if value {
		return 1
	}
	return 0
}

func ellipsize(text string, width int) string {
	if width < 1 || uniseg.StringWidth(text) <= width {
		return text
	}
	if width == 1 {
		return "…"
	}
	var out strings.Builder
	used := 0
	graphemes := uniseg.NewGraphemes(text)
	for graphemes.Next() {
		cluster := graphemes.Str()
		clusterWidth := uniseg.StringWidth(cluster)
		if used+clusterWidth > width-1 {
			break
		}
		out.WriteString(cluster)
		used += clusterWidth
	}
	return out.String() + "…"
}

func mergePages(a, b []notion.Page) []notion.Page {
	out := []notion.Page{}
	seen := map[string]bool{}
	for _, set := range [][]notion.Page{a, b} {
		for _, p := range set {
			if !seen[p.ID] {
				seen[p.ID] = true
				out = append(out, p)
			}
		}
	}
	return out
}

func workspacePages(pages []notion.Page) []notion.Page {
	roots := make([]notion.Page, 0, len(pages))
	for _, p := range pages {
		if p.ParentKind == "workspace" {
			roots = append(roots, p)
		}
	}
	return roots
}

func collectWorkspace(ctx context.Context, backend notion.Backend, progress func([]notion.Page, string, int)) ([]notion.Page, int, string, error) {
	roots, _, scanned, cursor, err := collectWorkspaceIndex(ctx, backend, func(roots, _ []notion.Page, cursor string, scanned int) {
		if progress != nil {
			progress(roots, cursor, scanned)
		}
	})
	return roots, scanned, cursor, err
}

func collectWorkspaceIndex(ctx context.Context, backend notion.Backend, progress func([]notion.Page, []notion.Page, string, int)) ([]notion.Page, []notion.Page, int, string, error) {
	found, all := []notion.Page{}, []notion.Page{}
	cursor := ""
	seenCursors := map[string]bool{}
	pagesScanned := 0
	for {
		l, err := backend.Search(ctx, "", cursor)
		if err != nil {
			return found, all, pagesScanned, cursor, err
		}
		pagesScanned += len(l.Pages)
		all = mergePages(all, l.Pages)
		found = mergePages(found, workspacePages(l.Pages))
		next := l.Cursor
		if progress != nil {
			progress(append([]notion.Page(nil), found...), append([]notion.Page(nil), all...), next, pagesScanned)
		}
		if next == "" {
			return found, all, pagesScanned, "", nil
		}
		if next == cursor || seenCursors[next] {
			return found, all, pagesScanned, "", fmt.Errorf("Notion repeated a workspace search cursor")
		}
		seenCursors[next] = true
		cursor = next
	}
}

func (a *app) scanWorkspace(ctx context.Context, gen int, known []notion.Page) {
	found, all, pagesScanned, cursor, err := collectWorkspaceIndex(ctx, a.backend, func(batch, indexed []notion.Page, next string, _ int) {
		a.ui.QueueUpdateDraw(func() {
			if gen != a.listGen {
				return
			}
			a.listed = mergePages(batch, known)
			a.state.Pages = mergePages(indexed, a.state.Pages)
			a.cursor = next
			a.rebuildList()
			if !a.syncing() && time.Now().After(a.noticeUntil) {
				a.message(fmt.Sprintf("Indexing workspace… %d roots found", len(batch)), false)
			}
		})
	})
	if err != nil {
		a.ui.QueueUpdateDraw(func() {
			if gen != a.listGen {
				return
			}
			a.backgroundResult(err)
			a.listing = false
			a.listed = mergePages(found, known)
			a.cursor = cursor
			a.state.Pages = mergePages(all, a.state.Pages)
			a.persistState()
			a.rebuildList()
			a.message(err.Error()+" · indexed workspace pages remain available", true)
		})
		return
	}
	a.ui.QueueUpdateDraw(func() {
		if gen != a.listGen {
			return
		}
		a.backgroundResult(nil)
		a.listing = false
		a.query, a.container = "", ""
		a.listed = found
		a.cursor = ""
		a.state.WorkspaceIndexed = time.Now()
		a.state.Pages = mergePages(all, a.state.Pages)
		a.listCache["|"] = cachedList{notion.Listing{Pages: found}, time.Now()}
		a.persistState()
		a.rebuildList()
		if !a.syncing() && time.Now().After(a.noticeUntil) {
			a.message(fmt.Sprintf("Workspace indexed · %d roots from %d accessible pages", len(found), pagesScanned), false)
		}
	})
}

func (a *app) loadList(query, container string, more, force bool) {
	a.startupListPending = false
	if more && (a.cursor == "" || a.listing) {
		a.sidebarExpanded = true
		a.rebuildList()
		return
	}
	if more {
		a.sidebarExpanded = true
	} else if query == "" && container == "" {
		a.sidebarExpanded = false
	}
	key := container + "|" + query
	if !more && !force {
		if c, ok := a.listCache[key]; ok && time.Since(c.at) < time.Minute {
			if a.listCancel != nil {
				a.listCancel()
			}
			a.listGen++
			a.listing = false
			a.query = query
			a.container = container
			a.listed = c.list.Pages
			a.cursor = c.list.Cursor
			a.rebuildList()
			a.message("Cached results · Enter a page to open", false)
			return
		}
	}
	if a.listCancel != nil {
		a.listCancel()
	}
	ctx, cancel := context.WithCancel(a.ctx)
	a.listCancel = cancel
	a.listGen++
	gen := a.listGen
	a.listing = true
	knownRoots := workspacePages(a.state.Pages)
	workspace := query == "" && container == "" && !a.demo
	fullWorkspaceScan := workspace && !more && (force || a.state.WorkspaceIndexed.IsZero() || time.Since(a.state.WorkspaceIndexed) >= 24*time.Hour)
	if workspace && !more && !force && !fullWorkspaceScan {
		a.listing = false
		a.query, a.container, a.cursor = "", "", ""
		a.listed = knownRoots
		a.rebuildList()
		return
	}
	if fullWorkspaceScan {
		a.query, a.container = "", ""
		a.message("Indexing accessible workspace roots…", false)
		go a.scanWorkspace(ctx, gen, knownRoots)
		return
	}
	cursor := ""
	if more {
		cursor = a.cursor
	}
	a.message("Finding pages…", false)
	go func() {
		var l notion.Listing
		var err error
		if container != "" {
			l, err = a.backend.Query(ctx, container, cursor)
		} else {
			l, err = a.backend.Search(ctx, query, cursor)
		}
		a.ui.QueueUpdateDraw(func() {
			if gen != a.listGen {
				return
			}
			a.listing = false
			if err != nil {
				a.message(err.Error()+" · cached pages remain available", true)
				return
			}
			a.query = query
			a.container = container
			pages := l.Pages
			if workspace {
				pages = workspacePages(pages)
			}
			if more {
				a.listed = mergePages(a.listed, pages)
			} else if workspace {
				a.listed = mergePages(pages, knownRoots)
			} else {
				a.listed = pages
			}
			a.cursor = l.Cursor
			if workspace && !a.state.WorkspaceIndexed.IsZero() && time.Since(a.state.WorkspaceIndexed) < 24*time.Hour {
				a.cursor = ""
			}
			a.listCache[key] = cachedList{notion.Listing{Pages: a.listed, Cursor: a.cursor}, time.Now()}
			a.state.Pages = mergePages(a.listed, a.state.Pages)
			a.persistState()
			a.rebuildList()
			a.message(fmt.Sprintf("%d results · Enter opens · p pins · n creates a child · m loads more", len(a.listed)), false)
		})
	}()
}

func (a *app) open(p notion.Page) {
	a.touchActivity()
	delete(a.refreshDelay, p.ID)
	if known, ok := a.knownPage(p.ID); ok && known.InTrash {
		a.openPageMenu(known)
		return
	}
	if p.InTrash {
		a.message("Page is in Trash · restore it from the page menu", false)
		return
	}
	if p.Kind == "database" {
		a.openDatabase(p.ID)
		return
	}
	a.state.Recents = mergePages([]notion.Page{p}, a.state.Recents)
	if len(a.state.Recents) > 30 {
		a.state.Recents = a.state.Recents[:30]
	}
	a.rebuildList()
	a.cancelFetch()
	if p.Kind == "data_source" {
		a.persistState()
		a.openDatabaseSource(p)
		return
	}
	if p.ID == a.active && a.docs[p.ID] != nil {
		a.ui.SetFocus(a.editor)
		return
	}
	if previous := a.docs[a.active]; previous != nil {
		a.viewStates[a.active] = pageView{a.editor.snapshot(), a.editor.undo, a.editor.redo}
		if !a.navigating {
			a.back = append(a.back, previous.Page)
			a.forward = nil
		}
	}
	a.active = p.ID
	// Clear the previous page and its undo history before a possibly slow fetch.
	a.setting = true
	a.editor.SetText("", false).SetPlaceholder("Opening page…")
	a.setting = false
	a.editor.SetDisabled(true)
	a.ui.SetFocus(a.editor)
	for list, pages := range map[*tview.List][]notion.Page{a.favorites: a.favoritePages, a.recents: a.recentPages, a.list: a.visible} {
		for i, item := range pages {
			if item.ID == p.ID {
				list.SetCurrentItem(i)
				break
			}
		}
	}
	a.setTitle(p.Title)
	a.setBreadcrumb(p)
	if a.breadcrumbCancel != nil {
		a.breadcrumbCancel()
	}
	a.state.Last = p.ID
	a.persistState()
	d := a.docs[p.ID]
	if d == nil {
		if cached, err := a.store.LoadDoc(p.ID); err == nil {
			d = &cached
			a.docs[p.ID] = d
		} else if !os.IsNotExist(err) {
			a.message("Cannot read cached draft: "+err.Error(), true)
			a.editor.SetPlaceholder("Could not read this page's local draft.")
			return
		}
	}
	if d != nil {
		a.showDoc(d)
		if a.needsSync(p.ID) {
			a.message("Recovered local draft · syncing automatically after a short pause", false)
			a.resolveBreadcrumb(p)
			return
		}
		// Cached content is shown immediately, then always validated in the
		// background. A cache hit must never hide a Notion-side edit.
		a.fetchContent(p, true)
		return
	}
	a.fetch(p)
}

func (a *app) fetch(p notion.Page) {
	a.fetchContent(p, false)
}

func (a *app) fetchContent(p notion.Page, quiet bool) {
	if !quiet {
		delete(a.refreshDelay, p.ID)
	}
	a.cancelFetch()
	ctx, cancel := context.WithCancel(a.ctx)
	a.fetchCancel = cancel
	a.fetchPage = p.ID
	a.fetchSeq++
	sequence := a.fetchSeq
	// A read is only applicable to the exact local base from which it began.
	// Typing followed by undo or a completed save may make Dirty false again.
	initial := a.docs[p.ID]
	initialBase := ""
	if initial != nil {
		initialBase = initial.Base.Markdown
	}
	a.fetching[p.ID] = true
	a.remoteChecked[p.ID] = time.Now()
	if !quiet {
		a.message("Opening "+p.Title+"…", false)
	}
	go func() {
		c, err := a.backend.Read(ctx, p.ID)
		a.ui.QueueUpdateDraw(func() {
			if sequence != a.fetchSeq {
				return
			}
			a.backgroundResult(err)
			a.remoteChecked[p.ID] = time.Now()
			// Parent discovery can take several requests; start it only after
			// the document read, using cached breadcrumbs in the meantime.
			if err == nil && a.active == p.ID {
				a.resolveBreadcrumb(p)
			}
			delete(a.fetching, p.ID)
			a.fetchCancel = nil
			a.fetchPage = ""
			if err != nil {
				if quiet {
					if a.active == p.ID && time.Now().After(a.noticeUntil) {
						a.message("Notion refresh delayed · will retry automatically", false)
					}
				} else {
					a.message(err.Error(), true)
				}
				if a.active == p.ID && a.docs[p.ID] == nil {
					a.editor.SetPlaceholder("Could not load this page. Open it again to retry.")
				}
				return
			}
			if d := a.docs[p.ID]; d != nil && a.needsSync(p.ID) {
				return
			}
			if current := a.docs[p.ID]; current != initial || current != nil && current.Base.Markdown != initialBase {
				return
			}
			if old := a.docs[p.ID]; old != nil && old.Base.Editable() && !c.Editable() {
				if !quiet || a.active == p.ID {
					a.message("Notion returned an incomplete page · keeping the complete cached copy", false)
				}
				return
			}
			if old := a.docs[p.ID]; old != nil && old.Base.Markdown == c.Markdown && old.Base.Editable() == c.Editable() {
				a.refreshDelay[p.ID] = min(max(15*time.Second, a.refreshDelay[p.ID]*2), time.Minute)
				old.Base = c
				old.Text = c.Markdown
				old.Fetched = time.Now()
				return
			}
			delete(a.refreshDelay, p.ID)
			if err := a.drafts.Flush(p.ID); err != nil {
				a.message("Local draft: "+err.Error(), true)
				return
			}
			if old := a.docs[p.ID]; old != nil {
				if a.active == p.ID {
					a.viewStates[p.ID] = pageView{a.editor.snapshot(), append([]richSnapshot(nil), a.editor.undo...), append([]richSnapshot(nil), a.editor.redo...)}
				}
				if err := a.store.SaveRevision(*old, "Before refresh"); err != nil {
					a.message("Refresh snapshot: "+err.Error(), true)
					return
				}
			}
			d := &notion.Doc{Page: p, Base: c, Text: c.Markdown, Fetched: time.Now()}
			if err := a.store.SaveRevision(*d, "Fetched"); err != nil {
				a.message("Refresh snapshot: "+err.Error(), true)
				return
			}
			a.drafts.Put(*d)
			if err := a.drafts.Flush(p.ID); err != nil {
				a.message(err.Error(), true)
				return
			}
			a.docs[p.ID] = d
			if a.active == p.ID {
				a.showDoc(d)
				if c.Editable() {
					if quiet {
						a.message("Updated from Notion", false)
					} else {
						a.message("Refreshed from Notion", false)
					}
				} else {
					a.message("Page incomplete · edits stay local until the full page is available", true)
				}
			}
		})
	}()
}

func (a *app) cancelFetch() {
	if a.fetchCancel != nil {
		a.fetchCancel()
		a.fetchCancel = nil
	}
	if a.fetchPage != "" {
		delete(a.fetching, a.fetchPage)
		a.fetchPage = ""
	}
	a.fetchSeq++
}

func (a *app) showDoc(d *notion.Doc) {
	duplicateRetry := notion.IsDuplicatedTableAttempt(d.Pending) || notion.IsRepeatedInsertionAttempt(d.Base, d.Pending)
	if d.Dirty && (d.Pending == nil || duplicateRetry) {
		if recovered := notion.RepairNewDraftTables(d.Base.Markdown, d.Text); recovered != d.Text || duplicateRetry {
			if err := a.store.SaveRevision(*d, "Before repairing draft and retry state"); err != nil {
				a.message("Could not back up draft: "+err.Error(), true)
			} else {
				d.Text = recovered
				d.Pending = nil
				a.drafts.Put(*d)
				a.blocked[d.Page.ID] = false
				a.changed[d.Page.ID] = time.Now()
				a.message("Recovered draft · original kept in revisions", false)
			}
		}
	}
	a.rememberPeople(peopleFromMarkdown(d.Text)...)
	a.setting = true
	view, restoreView := a.viewStates[d.Page.ID]
	oldVisible := ""
	if restoreView {
		oldVisible = richDisplay(parseRich(view.snapshot.text, a.editor.label))
	}
	if a.editor.GetText() != d.Text {
		a.editor.SetText(d.Text, false)
	}
	if restoreView {
		a.editor.anchor = mapTextOffset(oldVisible, a.editor.visible, view.snapshot.anchor)
		a.editor.head = mapTextOffset(oldVisible, a.editor.visible, view.snapshot.head)
		a.editor.visualRow = -1
		a.editor.syncNativeSelection()
		a.editor.SetOffset(view.snapshot.row, 0)
		if view.snapshot.text == d.Text {
			a.editor.visualRow = view.snapshot.visual
			a.editor.undo, a.editor.redo = view.undo, view.redo
		}
	}
	a.editor.SetPlaceholder("Write something…")
	a.editor.SetDisabled(false)
	a.setting = false
	if !d.Base.Editable() {
		a.blocked[d.Page.ID] = true
	}
	a.setTitle(d.Page.Title)
	a.setBreadcrumb(d.Page)
}

func (a *app) setTitle(title string) {
	a.titleOriginal = title
	if a.ui.GetFocus() != a.title || !a.renaming {
		a.title.SetText(title)
	}
}

func (a *app) cancelTitle() {
	a.titleCancel = true
	a.title.SetText(a.titleOriginal)
	a.ui.SetFocus(a.editor)
}

func (a *app) commitTitle() {
	d := a.docs[a.active]
	title := strings.TrimSpace(a.title.GetText())
	if d == nil || title == "" || title == d.Page.Title || a.renaming {
		return
	}
	backend, ok := a.backend.(notion.RenameBackend)
	if !ok {
		a.message("This backend cannot rename pages", true)
		return
	}
	a.renaming = true
	page := d.Page
	a.message("Renaming…", false)
	go func() {
		renamed, err := backend.Rename(a.ctx, page, title)
		a.ui.QueueUpdateDraw(func() { a.finishRename(title, renamed, err) })
	}()
}

func (a *app) finishRename(sent string, page notion.Page, err error) {
	a.renaming = false
	if err != nil {
		if a.quitting {
			a.quitting = false
		}
		a.message("Rename: "+err.Error()+" · title kept for retry", true)
		return
	}
	a.renameCachedPage(page)
	if a.active == page.ID {
		a.titleOriginal = page.Title
		if a.title.GetText() == sent {
			a.title.SetText(page.Title)
		}
	}
	a.message("Renamed", false)
	if a.quitting {
		a.quit()
	}
}

func (a *app) renameCachedPage(page notion.Page) {
	if d := a.docs[page.ID]; d != nil {
		d.Page = page
		a.drafts.Put(*d)
	}
	update := func(ps []notion.Page) {
		for i := range ps {
			if ps[i].ID == page.ID {
				ps[i] = page
			}
		}
	}
	update(a.listed)
	update(a.visible)
	update(a.state.Pages)
	update(a.state.Pins)
	update(a.state.Recents)
	for key, cached := range a.listCache {
		update(cached.list.Pages)
		a.listCache[key] = cached
	}
	a.persistState()
	a.rebuildList()
}

func (a *app) pin() {
	var p notion.Page
	if selected, ok := a.selectedSidebarPage(); ok {
		p = selected
	} else if d := a.docs[a.active]; d != nil {
		p = d.Page
	} else {
		return
	}
	a.togglePin(p)
}

func (a *app) togglePin(p notion.Page) {
	for i, pin := range a.state.Pins {
		if pin.ID == p.ID {
			a.state.Pins = append(a.state.Pins[:i], a.state.Pins[i+1:]...)
			a.persistState()
			a.rebuildList()
			a.message("Unpinned "+p.Title, false)
			return
		}
	}
	a.state.Pins = append(a.state.Pins, p)
	a.persistState()
	a.rebuildList()
	a.message("Pinned "+p.Title, false)
}

func (a *app) refresh() {
	if d := a.docs[a.active]; d != nil {
		if a.needsSync(d.Page.ID) {
			a.compare()
			return
		}
		a.fetch(d.Page)
	} else {
		a.loadList(a.query, a.container, false, true)
	}
}

func (a *app) keys(e *tcell.EventKey) *tcell.EventKey {
	a.touchActivity()
	commentInput := a.commentState.view != nil && a.ui.GetFocus() == a.commentState.view.input
	switch a.ui.GetFocus().(type) {
	case *tview.InputField, *tview.TextArea:
		if e.Key() == tcell.KeyCtrlA {
			return tcell.NewEventKey(tcell.KeyCtrlL, 0, e.Modifiers())
		}
		if e.Key() == tcell.KeyCtrlZ && e.Modifiers()&tcell.ModShift != 0 {
			return tcell.NewEventKey(tcell.KeyCtrlY, 0, tcell.ModNone)
		}
		if e.Key() == tcell.KeyRune && e.Modifiers()&tcell.ModMeta != 0 {
			switch e.Rune() {
			case 'a':
				return tcell.NewEventKey(tcell.KeyCtrlL, 0, tcell.ModNone)
			case 'c':
				return tcell.NewEventKey(tcell.KeyCtrlQ, 0, tcell.ModNone)
			case 'x':
				return tcell.NewEventKey(tcell.KeyCtrlX, 0, tcell.ModNone)
			case 'v':
				return tcell.NewEventKey(tcell.KeyCtrlV, 0, tcell.ModNone)
			case 'z':
				if e.Modifiers()&tcell.ModShift != 0 {
					return tcell.NewEventKey(tcell.KeyCtrlY, 0, tcell.ModNone)
				}
				return tcell.NewEventKey(tcell.KeyCtrlZ, 0, tcell.ModNone)
			}
		}
		if (e.Key() == tcell.KeyLeft || e.Key() == tcell.KeyRight) && e.Modifiers()&tcell.ModMeta != 0 {
			key := tcell.KeyHome
			if e.Key() == tcell.KeyRight {
				key = tcell.KeyEnd
			}
			return tcell.NewEventKey(key, 0, e.Modifiers()&tcell.ModShift)
		}
		if (e.Key() == tcell.KeyLeft || e.Key() == tcell.KeyRight) && e.Modifiers()&tcell.ModAlt != 0 {
			return tcell.NewEventKey(e.Key(), 0, (e.Modifiers()&^tcell.ModAlt)|tcell.ModCtrl)
		}
	}
	if e.Key() == tcell.KeyCtrlQ {
		a.quit()
		return nil
	}
	if e.Key() == tcell.KeyCtrlC && a.ui.GetFocus() != a.editor && !commentInput {
		a.quit()
		return nil
	}
	if e.Key() == tcell.KeyCtrlK || e.Key() == tcell.KeyRune && e.Rune() == 'k' && e.Modifiers()&tcell.ModMeta != 0 {
		if a.palette != nil {
			a.closeModal()
		} else if !a.modal {
			a.openPalette("")
		}
		return nil
	}
	if e.Key() == tcell.KeyCtrlBackslash && !a.modal {
		a.toggleSidebar()
		return nil
	}
	if e.Key() == tcell.KeyRune && e.Modifiers()&tcell.ModMeta != 0 {
		switch e.Rune() {
		case '[':
			a.navigate(true)
			return nil
		case ']':
			a.navigate(false)
			return nil
		case '\\':
			a.toggleSidebar()
			return nil
		}
	}
	if a.modal {
		if e.Key() == tcell.KeyEscape {
			a.closeModal()
			return nil
		}
		return e
	}
	switch e.Key() {
	case tcell.KeyCtrlC:
		if a.ui.GetFocus() == a.editor {
			return tcell.NewEventKey(tcell.KeyCtrlQ, 0, tcell.ModNone)
		}
		a.quit()
		return nil
	case tcell.KeyCtrlP:
		a.openPalette("")
		return nil
	case tcell.KeyCtrlN:
		a.newNote(false, false)
		return nil
	case tcell.KeyCtrlO:
		a.openURL()
		return nil
	case tcell.KeyCtrlS:
		a.save(a.active, true)
		return nil
	case tcell.KeyCtrlF:
		a.findInPage()
		return nil
	case tcell.KeyCtrlR:
		a.revisions()
		return nil
	case tcell.KeyEscape:
		if a.ui.GetFocus() == a.title {
			a.cancelTitle()
			return nil
		}
		a.ui.SetFocus(a.sidebarFocus())
		return nil
	case tcell.KeyBacktab:
		if a.ui.GetFocus() == a.editor {
			return e
		}
		if a.sidebarFocused() {
			a.ui.SetFocus(a.editor)
		} else {
			a.ui.SetFocus(a.sidebarFocus())
		}
		return nil
	}
	if e.Key() == tcell.KeyRune && e.Rune() == 'k' && e.Modifiers()&tcell.ModAlt != 0 {
		return tcell.NewEventKey(tcell.KeyCtrlK, 0, tcell.ModNone)
	}
	if e.Modifiers()&tcell.ModAlt != 0 && (e.Key() == tcell.KeyLeft || e.Key() == tcell.KeyRight) && a.ui.GetFocus() != a.editor {
		a.navigate(e.Key() == tcell.KeyLeft)
		return nil
	}
	return e
}

func (a *app) quit() {
	for _, d := range a.docs {
		if a.needsSync(d.Page.ID) {
			a.drafts.Put(*d)
		}
	}
	if err := a.drafts.FlushAll(); err != nil {
		a.message("Cannot quit: local draft failed: "+err.Error(), true)
		a.quitting = false
		return
	}
	if a.syncing() || a.creating || a.renaming || a.trashBusy || a.commentsSending() {
		a.quitting = true
		a.message("Finishing current sync before closing…", false)
		return
	}
	a.cancel()
	a.ui.Stop()
}

func (a *app) overlay(p tview.Primitive, width, height int) {
	_, _, w, h := a.layers.GetRect()
	width, height = min(width, max(20, w-2)), min(height, max(8, h-2))
	center := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(nil, 0, 1, false).AddItem(tview.NewFlex().AddItem(nil, 0, 1, false).AddItem(p, width, 0, true).AddItem(nil, 0, 1, false), height, 0, true).AddItem(nil, 0, 1, false)
	dimModalBackground(center)
	a.modal = true
	a.returnFocus = a.ui.GetFocus()
	a.layers.AddPage("modal", center, true, true)
	a.ui.SetFocus(p)
}

func dimModalBackground(layer *tview.Flex) {
	layer.SetDrawFunc(func(screen tcell.Screen, x, y, width, height int) (int, int, int, int) {
		for row := y; row < y+height; row++ {
			for column := x; column < x+width; column++ {
				main, combining, style, cellWidth := screen.GetContent(column, row)
				screen.SetContent(column, row, main, combining, style.Dim(true))
				column += max(1, cellWidth) - 1
			}
		}
		return x, y, width, height
	})
}

func (a *app) closeModal() {
	a.closeComments()
	if a.database != nil {
		view := a.database
		a.database = nil
		if view.cancel != nil {
			view.cancel()
		}
		a.layers.RemovePage("modal")
		a.modal = false
		if a.active != "" {
			a.ui.SetFocus(a.editor)
		} else {
			a.ui.SetFocus(a.sidebarFocus())
		}
		return
	}
	if a.slash != nil {
		pos := a.slash.pos
		a.slash = nil
		a.layers.RemovePage("modal")
		a.modal = false
		a.ui.SetFocus(a.editor)
		a.editor.Select(pos, pos).Replace(pos, pos, "/")
		return
	}
	if a.mention != nil {
		mention := a.mention
		if mention.cancel != nil {
			mention.cancel()
		}
		a.mention = nil
		a.layers.RemovePage("modal")
		a.modal = false
		a.ui.SetFocus(a.editor)
		literal := "@" + mention.input.GetText()
		a.editor.Select(mention.pos, mention.pos).Replace(mention.pos, mention.pos, literal)
		return
	}
	if a.palette != nil {
		if a.palette.cancel != nil {
			a.palette.cancel()
		}
		a.palette = nil
	}
	a.layers.RemovePage("modal")
	a.modal = false
	if a.returnFocus != nil {
		a.ui.SetFocus(a.returnFocus)
	} else if a.active != "" {
		a.ui.SetFocus(a.editor)
	} else {
		a.ui.SetFocus(a.sidebarFocus())
	}
}

func (a *app) newNote(copyText, child bool) {
	if a.creating {
		a.message("Creating a note…", false)
		return
	}
	parent := a.parent
	if child {
		if a.container != "" {
			parent = "data-source:" + a.container
		} else if a.active != "" {
			parent = "page:" + a.active
		}
	}
	text := ""
	title := ""
	if copyText {
		if d := a.docs[a.active]; d != nil {
			text = d.Text
			title = d.Page.Title + " (copy)"
		}
	}
	parent, title, text = a.recoverCreate(parent, title, text)
	f := tview.NewForm().SetButtonStyle(quiet).SetButtonActivatedStyle(selection).AddInputField("Title", title, 38, nil, nil).AddInputField("Parent", parent, 38, nil, nil)
	f.SetBorder(true).SetTitle(" New note · blank parent = workspace ")
	f.AddButton("Create", func() {
		title := strings.TrimSpace(f.GetFormItem(0).(*tview.InputField).GetText())
		parent := strings.TrimSpace(f.GetFormItem(1).(*tview.InputField).GetText())
		if title == "" {
			return
		}
		if parent != "" && !regexp.MustCompile(`^(page|data-source):[a-fA-F0-9-]{32,36}$`).MatchString(parent) {
			a.message("Parent: page:<uuid> or data-source:<uuid>", true)
			return
		}
		intent, err := a.store.BeginCreate(parent, title, text)
		if err != nil {
			a.message("Create blocked: "+err.Error()+" · Ctrl+K → Pending writes", true)
			return
		}
		a.closeModal()
		a.creating = true
		a.message("Creating "+title+"…", false)
		go func() {
			p, c, err := a.backend.Create(a.ctx, parent, title, text)
			a.ui.QueueUpdateDraw(func() {
				a.creating = false
				var intentErr error
				if p.ID != "" {
					intentErr = a.store.CompleteIntent(intent.ID)
					a.state.Pages = mergePages([]notion.Page{p}, a.state.Pages)
					a.listed = mergePages([]notion.Page{p}, a.listed)
					a.persistState()
					a.listCache = map[string]cachedList{}
					if err == nil {
						d := &notion.Doc{Page: p, Base: c, Text: c.Markdown, Fetched: time.Now()}
						a.docs[p.ID] = d
						a.drafts.Put(*d)
						if e := a.drafts.Flush(p.ID); e != nil {
							a.message(e.Error(), true)
						}
					}
					a.sidebarFilter = ""
					a.rebuildList()
					a.open(p)
				}
				if err != nil {
					if p.ID == "" {
						a.message("Create outcome unknown · request kept in Ctrl+K → Pending writes: "+err.Error(), true)
					} else {
						a.message("Page created; loading delayed: "+err.Error(), true)
					}
				}
				if intentErr != nil {
					a.message("Created, but pending record could not be cleared: "+intentErr.Error(), true)
				}
				if a.quitting {
					a.quit()
				}
			})
		}()
	}).AddButton("Cancel", a.closeModal)
	a.overlay(f, 68, 11)
}

var pageID = regexp.MustCompile(`(?i)[a-f0-9]{8}-?[a-f0-9]{4}-?[a-f0-9]{4}-?[a-f0-9]{4}-?[a-f0-9]{12}`)

func (a *app) openURL() {
	f := tview.NewForm().SetButtonStyle(quiet).SetButtonActivatedStyle(selection).AddInputField("URL / ID", "", 48, nil, nil)
	f.SetBorder(true).SetTitle(" Open a Notion page ")
	f.AddButton("Open", func() {
		input := f.GetFormItem(0).(*tview.InputField).GetText()
		id := pageID.FindString(strings.Split(input, "?")[0])
		if id == "" {
			a.message("Paste a Notion page URL or UUID", true)
			return
		}
		a.closeModal()
		a.message("Opening page…", false)
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
	}).AddButton("Cancel", a.closeModal)
	a.overlay(f, 68, 9)
}

func (a *app) compare() {
	if a.syncing() {
		a.message("Wait for the current sync before comparing", false)
		return
	}
	d := a.docs[a.active]
	if d == nil {
		return
	}
	id := d.Page.ID
	a.blocked[id] = true
	a.message("Reading remote version…", false)
	go func() {
		remote, err := a.backend.Read(a.ctx, id)
		a.ui.QueueUpdateDraw(func() {
			if err != nil {
				a.message(err.Error(), true)
				return
			}
			d := a.docs[id]
			local := tview.NewTextView().SetText(d.Text).SetWrap(true)
			local.SetBorder(true).SetTitle(" Local draft ")
			r := tview.NewTextView().SetText(remote.Markdown).SetWrap(true)
			r.SetBorder(true).SetTitle(" Notion ")
			useNotion := func() {
				if err := a.drafts.Flush(id); err != nil {
					a.message("Local draft: "+err.Error(), true)
					return
				}
				if err := a.store.SaveRevision(*d, "Before using Notion version"); err != nil {
					a.message("Snapshot: "+err.Error(), true)
					return
				}
				next := *d
				next.Base = remote
				next.Text = remote.Markdown
				next.Dirty = false
				next.Pending = nil
				next.Fetched = time.Now()
				if err := a.store.SaveRevision(next, "Notion version"); err != nil {
					a.message("Snapshot: "+err.Error(), true)
					return
				}
				a.drafts.Put(next)
				if err := a.drafts.Flush(id); err != nil {
					a.message(err.Error(), true)
					return
				}
				*d = next
				a.blocked[id] = false
				a.closeModal()
				if a.active == id {
					a.showDoc(d)
				}
				a.rebuildList()
				a.message("Loaded Notion version", false)
			}
			buttons := tview.NewFlex().AddItem(a.button("Keep draft", a.closeModal), 0, 1, true).AddItem(a.button("Use Notion version", useNotion), 0, 1, false).AddItem(a.button("Save draft as copy", func() { a.closeModal(); a.open(d.Page); a.newNote(true, false) }), 0, 1, false)
			versions := tview.NewFlex().AddItem(local, 0, 1, true).AddItem(r, 0, 1, false)
			selectComparison(a.ui, versions, local, r, a.closeModal, useNotion)
			hint := tview.NewTextView().SetText("← / → or click to select · Enter keeps selection · Esc cancels").SetTextStyle(quiet).SetWrap(false)
			pane := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(versions, 0, 1, true).AddItem(hint, 1, 0, false).AddItem(buttons, 1, 0, false)
			a.overlay(pane, 100, 28)
		})
	}()
}

func (a *app) help() {
	text := `A NOTEBOOK, WITHOUT THE TAB

Mouse       Click pages and buttons; wheel scrolls each pane.
Editor      Click to place cursor; drag or Shift+click to select.
            Double-click selects a word. Paste works normally.

Ctrl+K/P    Pages and commands; type > to show only commands
            Select “Search Notion…” for a workspace search
Ctrl+O      Open a page URL or ID
Ctrl+N      New note (default parent or workspace)
n           New child page (when the sidebar has focus)
p           Pin / unpin the selected page
g / m       Recent pages / load more (sidebar)
← / →       Collapse / expand the selected page tree
Cmd+[ / ]   Back / forward; Cmd+\ toggles the sidebar
Ctrl+E      Move to end of line
Ctrl+S      Force an immediate sync (normally automatic)
Ctrl+Z/Y    Undo / redo in the editor
Ctrl+B      Bold; Alt+U/I/S/C underline / italic / strike / code
Ctrl+U      Delete to line start (also Cmd+Backspace in Ghostty)
Ctrl+C/X/V  Copy / cut / paste in the editor (macOS clipboard)
Ctrl+A/L    Select all text in text fields
Alt+K       Delete to end of line (Ctrl+K opens the palette)
@           Mention a person, today/tomorrow, or an exact date
Esc         Focus the sidebar; text is kept
Tab/⇧Tab    Indent / outdent blocks (⇧Tab returns from sidebar)
Ctrl+R      Local revisions
Ctrl+Q      Quit; unsynced drafts stay on disk

Page menu   Click ··· or right-click a sidebar page.
Sidebar     Drag its divider to resize; double-click to reset.
Palette     Refresh, save a copy, comments, outline, page status,
            full width, block actions, and all navigation commands.

Type / on an empty line, or a Markdown marker then Space, to create blocks.
Inline tables render as rows while their exact Notion source stays intact.
Native Notion embeds may appear as enhanced Markdown tags.
Databases open read-only table, board, list, and gallery views.
Use / to filter, v to change view, g to group, and s to sort.

Requests are serialized (650ms spacing). Clean active pages refresh every 5s.
Autosave waits 3s; transient failures start a fresh safe retry window.
Transient errors retry automatically; conflicts stay local and protected.
Drafts survive offline work and restart, then safely resume syncing.`
	v := tview.NewTextView().SetText(text).SetWrap(true).SetWordWrap(true)
	v.SetBorder(true).SetTitle(" ntty · Esc to close ").SetBorderPadding(1, 1, 2, 2)
	pane := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(v, 0, 1, true).AddItem(a.button("Close", a.closeModal), 1, 0, false)
	a.overlay(pane, 80, 35)
}
