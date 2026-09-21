package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/rivo/uniseg"
	"ntty/internal/notion"
	"ntty/internal/store"
)

type delayedSave struct {
	notion.Backend
	started  chan string
	release  chan struct{}
	searches atomic.Int32
}

type pagedSearch struct {
	notion.Backend
	pages map[string]notion.Listing
	seen  []string
}

type ambiguousMergedSave struct {
	notion.Backend
	calls atomic.Int32
}

type switchingReads struct {
	notion.Backend
	started  chan string
	canceled chan string
}

type backendOnly struct{ notion.Backend }

type databaseRowsBackend struct {
	notion.Backend
	rows []notion.Page
}

type refreshBackend struct {
	notion.Backend
	reads   atomic.Int32
	content atomic.Value
}

func (b *refreshBackend) Read(_ context.Context, id string) (notion.Content, error) {
	b.reads.Add(1)
	content := b.content.Load().(notion.Content)
	content.ID = id
	return content, nil
}

func (b *databaseRowsBackend) Query(context.Context, string, string) (notion.Listing, error) {
	return notion.Listing{Pages: b.rows}, nil
}

func (b *databaseRowsBackend) Read(_ context.Context, id string) (notion.Content, error) {
	return notion.Content{Object: "page_markdown", ID: id, Markdown: "# Row " + id}, nil
}

func withProperties(page notion.Page, properties ...notion.Property) notion.Page {
	data, _ := json.Marshal(properties)
	page.PropertyData = string(data)
	return page
}

func (b *switchingReads) Read(ctx context.Context, id string) (notion.Content, error) {
	b.started <- id
	if id == "first" {
		<-ctx.Done()
		b.canceled <- id
		return notion.Content{}, ctx.Err()
	}
	return notion.Content{Object: "page_markdown", ID: id, Markdown: "# " + id}, nil
}

func (b *ambiguousMergedSave) Save(_ context.Context, _ string, base, text string) (notion.Content, error) {
	if b.calls.Add(1) == 1 {
		remote := notion.Content{Object: "page_markdown", ID: "p", Markdown: "Remote\nMiddle\nLast"}
		return remote, &notion.SaveAttemptError{Err: errors.New("503 response lost"), Base: remote, Text: "Remote\nMiddle\nLocal"}
	}
	if base != "Remote\nMiddle\nLast" || text != "Remote\nMiddle\nLocal" {
		return notion.Content{}, fmt.Errorf("wrong retry pair: base=%q text=%q", base, text)
	}
	return notion.Content{Object: "page_markdown", ID: "p", Markdown: text}, nil
}

func (b *pagedSearch) Search(_ context.Context, _ string, cursor string) (notion.Listing, error) {
	b.seen = append(b.seen, cursor)
	return b.pages[cursor], nil
}

func (b *delayedSave) Search(ctx context.Context, query, cursor string) (notion.Listing, error) {
	b.searches.Add(1)
	return b.Backend.Search(ctx, query, cursor)
}

func (b *delayedSave) Save(ctx context.Context, id, base, text string) (notion.Content, error) {
	b.started <- text
	select {
	case <-b.release:
	case <-ctx.Done():
		return notion.Content{}, ctx.Err()
	}
	return b.Backend.Save(ctx, id, base, text)
}

func TestMouseEditingAndDraftsDuringSync(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b := &delayedSave{Backend: notion.NewDemo(nil), started: make(chan string, 1), release: make(chan struct{})}
	a := newApp(b, s, store.State{}, nil, "", true)
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	a.ui.SetScreen(screen)
	screen.SetSize(130, 42)
	done := make(chan error, 1)
	go func() { done <- a.run() }()
	defer func() {
		a.ui.QueueUpdateDraw(func() { a.cancel(); a.ui.Stop() })
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	await := func(test func() bool) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			ok := false
			a.ui.QueueUpdateDraw(func() { ok = test() })
			if ok {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("UI did not reach expected state")
	}
	await(func() bool { return len(a.visible) == 3 })
	// Opening a page focuses the sole editor. Ordinary typing works immediately.
	a.ui.QueueUpdateDraw(func() {
		x, y, _, _ := a.list.GetInnerRect()
		h := a.list.MouseHandler()
		focus := func(p tview.Primitive) { a.ui.SetFocus(p) }
		h(tview.MouseLeftClick, tcell.NewEventMouse(x+3, y, tcell.Button1, tcell.ModNone), focus)
	})
	await(func() bool { return a.docs["demo-welcome"] != nil })
	a.ui.QueueUpdateDraw(func() {
		if a.ui.GetFocus() != a.editor || a.editor.GetDisabled() {
			t.Error("page did not open directly in the editor")
		}
		tx, _, tw, _ := a.title.GetInnerRect()
		ex, _, ew, _ := a.editor.GetInnerRect()
		bx, _, _, _ := a.breadcrumb.GetInnerRect()
		if tx != bx+2 {
			t.Errorf("breadcrumb/title left edges differ: breadcrumb=%d title=%d", bx+2, tx)
		}
		if tx != ex || tx+tw+2 != ex+ew {
			t.Errorf("title/editor alignment: title %d..%d editor %d..%d", tx, tx+tw, ex, ex+ew)
		}
	})
	a.ui.QueueEvent(tcell.NewEventKey(tcell.KeyRune, 'e', tcell.ModNone))
	await(func() bool { return strings.HasPrefix(a.editor.GetText(), "# e") })
	a.ui.QueueUpdateDraw(func() { a.open(a.docs[a.active].Page) })
	a.ui.QueueEvent(tcell.NewEventKey(tcell.KeyCtrlZ, 0, tcell.ModNone))
	await(func() bool { return strings.HasPrefix(a.editor.GetText(), "# A") && !a.docs[a.active].Dirty })
	// The page menu no longer offers a read/write mode switch.
	a.ui.QueueEvent(tcell.NewEventMouse(127, 2, tcell.Button1, tcell.ModNone))
	a.ui.QueueEvent(tcell.NewEventMouse(127, 2, tcell.ButtonNone, tcell.ModNone))
	await(func() bool { return a.modal })
	a.ui.QueueEvent(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone))
	await(func() bool { return a.palette == nil && a.ui.GetFocus() == a.editor })
	// Ctrl+K replaces the editor's destructive kill-line binding and preserves focus.
	a.ui.QueueEvent(tcell.NewEventKey(tcell.KeyCtrlK, 0, tcell.ModNone))
	await(func() bool { return a.palette != nil })
	a.ui.QueueUpdateDraw(func() {
		a.palette.input.SetText("quick")
		if a.palette.items[0].label != "Quick notes" {
			t.Error("cached page not found")
		}
		if b.searches.Load() != 1 {
			t.Error("typing in the palette made API calls")
		}
	})
	// Only small foreground accents and native selection depart from defaults.
	a.ui.QueueUpdateDraw(func() {
		cells, _, _ := screen.GetContents()
		selectionFG, selectionBG, _ := selection.Decompose()
		for _, cell := range cells {
			fg, bg, _ := cell.Style.Decompose()
			if !((fg == tcell.ColorDefault || fg == tcell.ColorTeal || fg == tcell.ColorOlive) && bg == tcell.ColorDefault) && (fg != selectionFG || bg != selectionBG) {
				t.Errorf("fixed color leaked into the theme: %v %v", fg, bg)
				break
			}
		}
		x, y, width, _ := a.palette.input.GetRect()
		for col := x; col < x+width; col++ {
			r, _, _, _ := screen.GetContent(col, y-1)
			if r != ' ' {
				t.Errorf("document bleeds through palette padding at %d: %q", col, r)
				break
			}
		}
		a.searchPalette(a.palette, "quick")
	})
	await(func() bool { return a.palette.remoteQuery == "quick" })
	a.ui.QueueEvent(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone))
	await(func() bool { return a.palette == nil && a.ui.GetFocus() == a.editor })
	a.ui.QueueEvent(tcell.NewEventKey(tcell.KeyCtrlK, 0, tcell.ModNone))
	await(func() bool { return a.palette != nil })
	a.ui.QueueUpdateDraw(func() {
		a.palette.input.SetText("quick")
		a.searchPalette(a.palette, "quick")
		if b.searches.Load() != 2 {
			t.Error("palette did not reuse cached search")
		}
	})
	// Outside click dismisses the palette without clicking through to the document.
	a.ui.QueueEvent(tcell.NewEventMouse(0, 0, tcell.Button1, tcell.ModNone))
	a.ui.QueueEvent(tcell.NewEventMouse(0, 0, tcell.ButtonNone, tcell.ModNone))
	await(func() bool { return a.palette == nil && a.ui.GetFocus() == a.editor })
	a.ui.QueueUpdateDraw(func() {
		x, y, _, _ := a.editor.GetInnerRect()
		h := a.editor.MouseHandler()
		focus := func(p tview.Primitive) { a.ui.SetFocus(p) }
		h(tview.MouseLeftDown, tcell.NewEventMouse(x, y, tcell.Button1, tcell.ModNone), focus)
		h(tview.MouseLeftUp, tcell.NewEventMouse(x, y, tcell.ButtonNone, tcell.ModNone), focus)
		a.editor.InputHandler()(tcell.NewEventKey(tcell.KeyRune, 'X', tcell.ModNone), focus)
		if !strings.HasPrefix(a.editor.GetText(), "# X") {
			t.Error("mouse cursor placement failed")
		}
		// Native drag selection and wheel scrolling.
		h(tview.MouseLeftDown, tcell.NewEventMouse(x, y, tcell.Button1, tcell.ModNone), focus)
		h(tview.MouseMove, tcell.NewEventMouse(x+3, y, tcell.Button1, tcell.ModNone), focus)
		h(tview.MouseLeftUp, tcell.NewEventMouse(x+3, y, tcell.ButtonNone, tcell.ModNone), focus)
		selected, _, _ := a.editor.GetSelection()
		if selected != "XA " {
			t.Errorf("mouse selection: %q", selected)
		}
		a.editor.SetText(strings.Repeat("scroll\n", 80), false)
		h(tview.MouseScrollDown, tcell.NewEventMouse(x, y, tcell.WheelDown, tcell.ModNone), focus)
		row, _ := a.editor.GetOffset()
		if row < 1 {
			t.Error("mouse wheel did not scroll")
		}
		// The application-level hover router must keep the wheel in the pane
		// under the pointer instead of forwarding it to a sidebar list.
		a.editor.SetOffset(0, 0)
		sidebarBefore, _ := a.list.GetOffset()
		event := tcell.NewEventMouse(x, y, tcell.WheelDown, tcell.ModNone)
		a.ui.GetMouseCapture()(event, tview.MouseScrollDown)
		row, _ = a.editor.GetOffset()
		sidebarAfter, _ := a.list.GetOffset()
		if row < 1 || sidebarAfter != sidebarBefore {
			t.Errorf("global wheel routing: editor=%d sidebar=%d→%d", row, sidebarBefore, sidebarAfter)
		}
		a.editor.SetText("Hello café world\nSecond line\n", false)
	})
	// Exercise real double-click recognition, not just the widget's handler.
	var mouseX, mouseY int
	a.ui.QueueUpdateDraw(func() { x, y, _, _ := a.editor.GetInnerRect(); mouseX, mouseY = x+8, y })
	for i := 0; i < 2; i++ {
		a.ui.QueueEvent(tcell.NewEventMouse(mouseX, mouseY, tcell.Button1, tcell.ModNone))
		a.ui.QueueEvent(tcell.NewEventMouse(mouseX, mouseY, tcell.ButtonNone, tcell.ModNone))
	}
	await(func() bool { text, _, _ := a.editor.GetSelection(); return text == "café" })
	a.ui.QueueEvent(tcell.NewEventKey(tcell.KeyRune, 'é', tcell.ModNone))
	await(func() bool { return a.editor.GetText() == "Hello é world\nSecond line\n" })
	a.ui.QueueEvent(tcell.NewEventKey(tcell.KeyCtrlZ, 0, tcell.ModNone))
	await(func() bool { return a.editor.GetText() == "Hello café world\nSecond line\n" })
	a.ui.QueueUpdateDraw(func() {
		x, y, _, _ := a.editor.GetInnerRect()
		h := a.editor.MouseHandler()
		focus := func(p tview.Primitive) { a.ui.SetFocus(p) }
		h(tview.MouseLeftDown, tcell.NewEventMouse(x, y, tcell.Button1, tcell.ModNone), focus)
		h(tview.MouseLeftUp, tcell.NewEventMouse(x, y, tcell.ButtonNone, tcell.ModNone), focus)
		h(tview.MouseLeftDown, tcell.NewEventMouse(x+5, y, tcell.Button1, tcell.ModShift), focus)
		h(tview.MouseLeftUp, tcell.NewEventMouse(x+5, y, tcell.ButtonNone, tcell.ModShift), focus)
		text, _, _ := a.editor.GetSelection()
		if text != "Hello" {
			t.Errorf("Shift-click selection: %q", text)
		}
		a.editor.SetText("first edit", true)
		a.save(a.active, true)
	})
	select {
	case text := <-b.started:
		if text != "first edit" {
			t.Fatalf("wrong snapshot %q", text)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("save did not start")
	}
	a.ui.QueueUpdateDraw(func() { a.editor.SetText("newer typing", true) })
	close(b.release)
	await(func() bool { return !a.syncing() })
	a.ui.QueueUpdateDraw(func() {
		d := a.docs[a.active]
		if d.Text != "newer typing" || !d.Dirty || d.Base.Markdown != "first edit" {
			t.Errorf("sync clobbered newer edits: %+v", d)
		}
		disk, err := s.LoadDoc(d.Page.ID)
		if err != nil || disk.Text != "newer typing" || !disk.Dirty {
			t.Errorf("newer typing not persisted: %+v %v", disk, err)
		}
	})
}

func TestAppRetainsMergedAttemptAndAutosyncsAfterLostResponse(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	demo := notion.NewDemo(nil)
	b := &ambiguousMergedSave{Backend: demo}
	a := newApp(b, s, store.State{}, nil, "", true)
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	screen.SetSize(100, 30)
	a.ui.SetScreen(screen)
	done := make(chan error, 1)
	go func() { done <- a.ui.Run() }()
	defer func() {
		a.ui.QueueUpdateDraw(func() { a.cancel(); a.ui.Stop() })
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()

	a.ui.QueueUpdateDraw(func() {
		base := notion.Content{Object: "page_markdown", ID: "p", Markdown: "Before\nMiddle\nLast"}
		d := &notion.Doc{Page: notion.Page{ID: "p", Title: "Retry", Kind: "page"}, Base: base, Text: "Before\nMiddle\nLocal", Dirty: true}
		a.docs["p"], a.active = d, "p"
		a.showDoc(d)
		a.changed["p"] = time.Now().Add(-4 * time.Second)
		a.autoSave(time.Now())
	})
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		ready := false
		a.ui.QueueUpdateDraw(func() {
			d := a.docs["p"]
			ready = !a.syncing() && b.calls.Load() == 1 && d.Pending != nil && d.Base.Markdown == "Before\nMiddle\nLast" && d.Text == "Before\nMiddle\nLocal" && d.Dirty && !a.blocked["p"]
		})
		if ready {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	a.ui.QueueUpdateDraw(func() {
		a.changed["p"] = time.Now().Add(-4 * time.Second)
		a.autoSave(time.Now())
	})
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		clean := false
		a.ui.QueueUpdateDraw(func() { clean = !a.syncing() && b.calls.Load() == 2 && !a.docs["p"].Dirty })
		if clean {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("merged retry did not finish: calls=%d", b.calls.Load())
}

func TestShowDocMapsCursorAcrossRemoteRefresh(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := newApp(notion.NewDemo(nil), s, store.State{}, nil, "", true)
	defer a.cancel()
	page := notion.Page{ID: "p", Title: "Cursor", Kind: "page"}
	oldText := "one\ntwo cursor\nthree"
	old := &notion.Doc{Page: page, Base: notion.Content{Object: "page_markdown", ID: page.ID, Markdown: oldText}, Text: oldText}
	a.docs[page.ID], a.active = old, page.ID
	a.showDoc(old)
	position := strings.Index(oldText, "cursor") + len("cursor")
	a.editor.Select(position, position)
	a.viewStates[page.ID] = pageView{snapshot: a.editor.snapshot()}

	newText := "remote\n" + oldText
	refreshed := &notion.Doc{Page: page, Base: notion.Content{Object: "page_markdown", ID: page.ID, Markdown: newText}, Text: newText}
	a.docs[page.ID] = refreshed
	a.showDoc(refreshed)
	if a.editor.head != position+len("remote\n") || a.editor.anchor != a.editor.head {
		t.Fatalf("refresh moved cursor to %d,%d; want %d", a.editor.anchor, a.editor.head, position+len("remote\n"))
	}
}

func TestActiveCleanPageRefreshesFromNotionWithoutOverwritingDraft(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b := &refreshBackend{Backend: notion.NewDemo(nil)}
	b.content.Store(notion.Content{Object: "page_markdown", Markdown: "Remote update"})
	a := newApp(b, s, store.State{}, nil, "", false)
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	screen.SetSize(100, 30)
	a.ui.SetScreen(screen)
	done := make(chan error, 1)
	go func() { done <- a.ui.Run() }()
	defer func() {
		a.ui.QueueUpdateDraw(func() { a.cancel(); a.ui.Stop() })
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()

	page := notion.Page{ID: "refresh", Title: "Refresh", Kind: "page"}
	base := notion.Content{Object: "page_markdown", ID: page.ID, Markdown: "Cached"}
	a.ui.QueueUpdateDraw(func() {
		a.docs[page.ID] = &notion.Doc{Page: page, Base: base, Text: base.Markdown}
		a.active = page.ID
		a.showDoc(a.docs[page.ID])
		a.remoteChecked[page.ID] = time.Now().Add(-remoteRefreshInterval)
		a.autoRefresh(time.Now())
	})
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		updated := false
		a.ui.QueueUpdateDraw(func() { updated = a.docs[page.ID].Text == "Remote update" && a.editor.GetText() == "Remote update" })
		if updated {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	var updated bool
	a.ui.QueueUpdateDraw(func() { updated = a.docs[page.ID].Text == "Remote update" })
	if !updated {
		t.Fatal("active clean page did not refresh from Notion")
	}

	reads := b.reads.Load()
	b.content.Store(notion.Content{Object: "page_markdown", Markdown: "Must not replace draft"})
	a.ui.QueueUpdateDraw(func() {
		a.editor.Replace(len(a.editor.visible), len(a.editor.visible), " local")
		a.remoteChecked[page.ID] = time.Now().Add(-remoteRefreshInterval)
		a.autoRefresh(time.Now())
	})
	time.Sleep(100 * time.Millisecond)
	a.ui.QueueUpdateDraw(func() {
		if b.reads.Load() != reads || a.docs[page.ID].Text != "Remote update local" || !a.docs[page.ID].Dirty {
			t.Errorf("refresh overwrote or needlessly read over a draft: reads=%d→%d doc=%+v", reads, b.reads.Load(), a.docs[page.ID])
		}
	})
}

func TestRenameCompletionKeepsNewTypingAndFailureDraft(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := newApp(notion.NewDemo(nil), s, store.State{}, nil, "", true)
	defer a.cancel()
	page := notion.Page{ID: "p", Title: "Old", Kind: "page"}
	a.docs[page.ID], a.active = &notion.Doc{Page: page}, page.ID
	a.state.Pages, a.state.Pins, a.listed = []notion.Page{page}, []notion.Page{page}, []notion.Page{page}
	a.listCache["cached"] = cachedList{list: notion.Listing{Pages: []notion.Page{page}}}
	a.titleOriginal = page.Title
	a.title.SetText("Draft")
	a.renaming = true
	a.finishRename("Draft", page, errors.New("offline"))
	if a.title.GetText() != "Draft" || a.renaming {
		t.Fatal("failed rename discarded the draft")
	}
	a.title.SetText("Newer typing")
	a.renaming = true
	renamed := page
	renamed.Title = "Saved snapshot"
	a.finishRename("Draft", renamed, nil)
	if a.title.GetText() != "Newer typing" || a.docs[page.ID].Page.Title != "Saved snapshot" {
		t.Fatal("rename completion clobbered in-flight typing")
	}
	if a.state.Pages[0].Title != renamed.Title || a.state.Pins[0].Title != renamed.Title || a.listed[0].Title != renamed.Title || a.listCache["cached"].list.Pages[0].Title != renamed.Title {
		t.Fatal("rename did not update cached page indexes")
	}
	if err := a.drafts.FlushAll(); err != nil {
		t.Fatal(err)
	}
}

func TestSlashBlockMenuFiltersEscapesAndRestoresCursor(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := newApp(notion.NewDemo(nil), s, store.State{}, nil, "", true)
	defer a.cancel()
	d := &notion.Doc{Page: notion.Page{ID: "slash-test", Title: "Slash", Kind: "page"}, Base: notion.Content{Object: "page_markdown", ID: "slash-test"}}
	a.docs[d.Page.ID], a.active = d, d.Page.ID
	a.editor.SetDisabled(false)
	a.editor.SetText("", false)
	focus := func(tview.Primitive) {}
	a.editor.InputHandler()(tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone), focus)
	if a.slash == nil || a.editor.GetText() != "" || d.Text != "" {
		t.Fatalf("slash query leaked into draft: editor=%q draft=%q", a.editor.GetText(), d.Text)
	}
	a.slash.input.SetText("heading 2")
	if len(a.slash.items) != 1 || a.slash.items[0].label != "Heading 2" {
		t.Fatalf("unexpected filtered blocks: %+v", a.slash.items)
	}
	a.closeModal()
	if a.editor.GetText() != "/" || d.Text != "/" {
		t.Fatalf("escape did not retain literal slash: editor=%q draft=%q", a.editor.GetText(), d.Text)
	}

	a.setting = true
	a.editor.SetText("\n", true)
	d.Text = "\n"
	a.setting = false
	a.editor.InputHandler()(tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone), focus)
	a.slash.input.SetText("todo")
	a.slash.items[0].run()
	if a.slash != nil || a.ui.GetFocus() != a.editor || a.editor.GetText() != "\n- [ ] " || !strings.HasSuffix(a.editor.visible, "☐ ") {
		t.Fatalf("block command failed: source=%q visible=%q", a.editor.GetText(), a.editor.visible)
	}

	a.setting = true
	a.editor.SetText("<empty-block/>", false)
	d.Text = "<empty-block/>"
	a.setting = false
	a.editor.InputHandler()(tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone), focus)
	a.slash.input.SetText("table")
	if len(a.slash.items) != 1 || a.slash.items[0].label != "Table" {
		t.Fatalf("/table was not offered: %+v", a.slash.items)
	}
	a.slash.items[0].run()
	if a.slash != nil || !strings.Contains(a.editor.GetText(), "<table ") || strings.Contains(a.editor.visible, "<td") {
		t.Fatalf("/table did not create a rendered Notion table: source=%q visible=%q", a.editor.GetText(), a.editor.visible)
	}
}

func TestIdleMaintenanceDoesNotRedrawOrFlickerCaret(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := newApp(notion.NewDemo(nil), s, store.State{}, nil, "", true)
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	a.ui.SetScreen(screen)
	screen.SetSize(100, 30)
	var draws atomic.Int32
	a.ui.SetAfterDrawFunc(func(tcell.Screen) { draws.Add(1) })
	done := make(chan error, 1)
	go func() { done <- a.run() }()
	defer func() {
		a.ui.QueueUpdate(func() { a.cancel(); a.ui.Stop() })
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()

	time.Sleep(500 * time.Millisecond) // Let startup requests and their draws settle.
	a.ui.QueueUpdate(func() {})
	draws.Store(0)
	time.Sleep(350 * time.Millisecond)
	if got := draws.Load(); got != 0 {
		t.Fatalf("idle housekeeping redrew the full interface %d times", got)
	}

	a.ui.QueueEvent(tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone))
	time.Sleep(100 * time.Millisecond)
	if draws.Load() == 0 {
		t.Fatal("input did not redraw promptly")
	}
}

func TestLogoStaysStillWhileSaving(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := newApp(notion.NewDemo(nil), s, store.State{}, nil, "", true)
	defer a.cancel()
	a.savingPage = "page"
	a.syncIndicator()
	first := a.logo.GetText(false)
	a.savingPage = ""
	a.syncIndicator()
	second := a.logo.GetText(false)
	if first != second {
		t.Fatalf("logo changed during sync: %q -> %q", first, second)
	}
	a.savingPage = ""
	a.syncIndicator()
	if strings.ContainsAny(a.logo.GetText(false), "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏") {
		t.Fatalf("sync animation continued while idle: %q", a.logo.GetText(false))
	}
}

func TestQuietSidebarIsCompactCappedAndExpandable(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pages := make([]notion.Page, 50)
	for i := range pages {
		pages[i] = notion.Page{ID: fmt.Sprintf("page-%02d", i), Title: fmt.Sprintf("A very long recent page title %02d", i), Kind: "page"}
	}
	a := newApp(notion.NewDemo(nil), s, store.State{Pages: pages}, nil, "", true)
	defer a.cancel()
	a.sidebarWidth = 24
	a.listed = pages
	a.rebuildList()
	if len(a.visible) != 20 || a.list.GetItemCount() != 20 {
		t.Fatalf("default sidebar should stay concise: visible=%d items=%d", len(a.visible), a.list.GetItemCount())
	}
	main, secondary := a.list.GetItemText(0)
	if !strings.HasSuffix(main, "…") || secondary != "" {
		t.Fatalf("sidebar row should be compact and ellipsized: main=%q secondary=%q", main, secondary)
	}

	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(30, 8)
	a.list.SetRect(0, 0, 24, 8)
	a.list.Focus(nil)
	a.list.Draw(screen)
	_, _, selectedStyle, _ := screen.GetContent(1, 0)
	selectedFG, selectedBG, selectedAttrs := selectedStyle.Decompose()
	wantFG, wantBG, wantAttrs := navigationSelection.Decompose()
	if selectedFG != wantFG || selectedBG != wantBG || selectedAttrs != wantAttrs {
		t.Fatalf("selected row did not use the terminal selection style: %v/%v attrs=%v, want %v/%v attrs=%v", selectedFG, selectedBG, selectedAttrs, wantFG, wantBG, wantAttrs)
	}
	_, _, blankStyle, _ := screen.GetContent(23, 0)
	_, _, blankAttrs := blankStyle.Decompose()
	if blankAttrs&(tcell.AttrReverse|tcell.AttrBold|tcell.AttrUnderline) != 0 {
		t.Fatalf("selected row leaked emphasis across blank space: attrs=%v", blankAttrs)
	}
	if r, _, _, _ := screen.GetContent(5, 1); r != 'A' {
		t.Fatal("second compact sidebar row was not drawn directly below the first")
	}
	a.list.SetOffset(7, 0)
	x, y, _, _ := a.list.GetInnerRect()
	if got := sidebarIndexAt(a.list, tcell.NewEventMouse(x+4, y+1, tcell.Button1, tcell.ModNone)); got != 8 {
		t.Fatalf("scrolled sidebar hit-test: %d", got)
	}
	if got := sidebarIndexAt(a.list, tcell.NewEventMouse(x+4, y-1, tcell.Button1, tcell.ModNone)); got != -1 {
		t.Fatalf("sidebar padding was clickable: %d", got)
	}
	a.list.SetOffset(0, 0)

	a.sidebarExpanded = true
	a.rebuildList()
	if len(a.visible) != len(pages) {
		t.Fatalf("expanded sidebar hid pages: %d/%d", len(a.visible), len(pages))
	}
	a.sidebarExpanded = false
	a.sidebarFilter = "page"
	a.rebuildList()
	if len(a.visible) != len(pages) {
		t.Fatalf("sidebar search hid matching pages: %d/%d", len(a.visible), len(pages))
	}
}

func TestSidebarTreeMouseKeyboardAndViewState(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := notion.Page{ID: "root", Title: "Root", Kind: "page", ParentKind: "workspace"}
	child := notion.Page{ID: "child", Title: "Child", Kind: "page", ParentKind: "page_id", ParentID: root.ID}
	grandchild := notion.Page{ID: "grandchild", Title: "Grandchild", Kind: "page", ParentKind: "page_id", ParentID: child.ID}
	a := newApp(notion.NewDemo(nil), s, store.State{Pages: []notion.Page{root, child, grandchild}}, nil, "", false)
	defer a.cancel()
	for _, page := range []notion.Page{root, child, grandchild} {
		a.docs[page.ID] = &notion.Doc{Page: page, Base: notion.Content{Object: "page_markdown", ID: page.ID, Markdown: page.Title}, Text: page.Title, Fetched: time.Now()}
	}
	a.listed = []notion.Page{root}
	a.sidebarWidth, a.sidebarHeight = 30, 20
	a.rebuildList()
	if len(a.visible) != 1 || !a.visibleHasChildren[0] {
		t.Fatalf("collapsed tree: pages=%+v children=%v", a.visible, a.visibleHasChildren)
	}
	a.list.SetRect(0, 0, 30, 8)
	x, y, _, _ := a.list.GetInnerRect()
	handler := a.list.MouseHandler()
	handler(tview.MouseLeftClick, tcell.NewEventMouse(x+2, y, tcell.Button1, tcell.ModNone), func(p tview.Primitive) { a.ui.SetFocus(p) })
	if len(a.visible) != 2 || a.visible[1].ID != child.ID || a.active != "" {
		t.Fatalf("tree disclosure opened a page or failed to expand: active=%q visible=%+v", a.active, a.visible)
	}
	// Clicking beyond the disclosure target opens the row directly.
	x, y, _, _ = a.list.GetInnerRect()
	handler(tview.MouseLeftClick, tcell.NewEventMouse(x+9, y+1, tcell.Button1, tcell.ModNone), func(p tview.Primitive) { a.ui.SetFocus(p) })
	if a.active != child.ID || a.editor.GetText() != child.Title {
		t.Fatalf("child row did not open in-app: active=%q text=%q", a.active, a.editor.GetText())
	}
	if len(a.breadcrumbPages) != 2 || !strings.Contains(a.breadcrumb.GetText(false), "Root  /  Child") {
		t.Fatalf("breadcrumb path: pages=%+v text=%q", a.breadcrumbPages, a.breadcrumb.GetText(false))
	}
	// Right expands the selected child, then right again enters its first child;
	// left returns to the parent and collapses it.
	a.list.SetCurrentItem(1)
	a.list.InputHandler()(tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModNone), func(tview.Primitive) {})
	if len(a.visible) != 3 {
		t.Fatalf("keyboard expansion: %+v", a.visible)
	}
	a.list.SetCurrentItem(2)
	a.list.InputHandler()(tcell.NewEventKey(tcell.KeyLeft, 0, tcell.ModNone), func(tview.Primitive) {})
	if a.list.GetCurrentItem() != 1 {
		t.Fatalf("left did not select parent: %d", a.list.GetCurrentItem())
	}

	// Per-page cursor and undo history survive switching.
	a.editor.Select(2, 2)
	a.editor.Replace(2, 2, "!")
	wantText := a.editor.GetText()
	a.open(root)
	a.open(child)
	_, _, cursor := a.editor.GetSelection()
	if a.editor.GetText() != wantText || cursor != 3 || len(a.editor.undo) == 0 {
		t.Fatalf("view state was not restored: text=%q cursor=%d undo=%d", a.editor.GetText(), cursor, len(a.editor.undo))
	}
}

func TestRapidPageSwitchCancelsOlderRead(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b := &switchingReads{Backend: notion.NewDemo(nil), started: make(chan string, 4), canceled: make(chan string, 1)}
	a := newApp(b, s, store.State{}, nil, "", true)
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	a.ui.SetScreen(screen)
	screen.SetSize(100, 30)
	done := make(chan error, 1)
	go func() { done <- a.run() }()
	defer func() {
		a.ui.QueueUpdateDraw(func() { a.cancel(); a.ui.Stop() })
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	first := notion.Page{ID: "first", Title: "First", Kind: "page"}
	second := notion.Page{ID: "second", Title: "Second", Kind: "page"}
	a.ui.QueueUpdateDraw(func() { a.open(first) })
	select {
	case id := <-b.started:
		if id != first.ID {
			t.Fatalf("first read: %q", id)
		}
	case <-time.After(time.Second):
		t.Fatal("first read did not start")
	}
	a.ui.QueueUpdateDraw(func() { a.open(second) })
	select {
	case id := <-b.canceled:
		if id != first.ID {
			t.Fatalf("canceled read: %q", id)
		}
	case <-time.After(time.Second):
		t.Fatal("older read was not canceled")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ready := false
		a.ui.QueueUpdateDraw(func() { ready = a.active == second.ID && a.docs[second.ID] != nil && a.editor.GetText() == "# second" })
		if ready {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("newer page did not win rapid navigation")
}

// TestLiveEditorAutosaveRoundTrip is opt-in and uses only the disposable page
// also used by internal/notion's live test. It exercises the actual editor,
// draft writer, idle autosave, remote read-back, and exact restoration.
func TestLiveEditorAutosaveRoundTrip(t *testing.T) {
	id := os.Getenv("NTTY_LIVE_PAGE_ID")
	if id == "" {
		t.Skip("set NTTY_LIVE_PAGE_ID to the disposable ntty test page")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	client := notion.NewClient()
	page, err := client.Page(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	id = page.ID
	original, err := client.Read(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(original.Markdown, "First block") != 1 {
		t.Fatal("disposable page is missing its unique First block anchor")
	}
	restored := false
	defer func() {
		if restored {
			return
		}
		current, readErr := client.Read(context.Background(), id)
		if readErr == nil && current.Markdown != original.Markdown {
			if _, restoreErr := client.Save(context.Background(), id, current.Markdown, original.Markdown); restoreErr != nil {
				t.Errorf("emergency restore: %v", restoreErr)
			}
		}
	}()

	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	doc := notion.Doc{Page: page, Base: original, Text: original.Markdown, Fetched: time.Now()}
	a := newApp(client, s, store.State{Pages: []notion.Page{page}}, nil, "", true)
	a.docs[id] = &doc
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	a.ui.SetScreen(screen)
	screen.SetSize(110, 32)
	done := make(chan error, 1)
	go func() { done <- a.run() }()
	stop := func() {
		a.ui.QueueUpdateDraw(func() { a.cancel(); a.ui.Stop() })
		if runErr := <-done; runErr != nil {
			t.Errorf("run: %v", runErr)
		}
	}
	stopped := false
	defer func() {
		if !stopped {
			stop()
		}
	}()

	a.ui.QueueUpdateDraw(func() {
		a.open(page)
		position := strings.Index(a.editor.visible, "First block") + len("First block")
		a.editor.Replace(position, position, " — editor-live")
		a.changed[id] = time.Now().Add(-4 * time.Second)
		a.autoSave(time.Now())
	})
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		synced := false
		a.ui.QueueUpdateDraw(func() {
			synced = !a.syncing() && a.docs[id] != nil && !a.docs[id].Dirty && strings.Contains(a.docs[id].Text, "First block — editor-live")
		})
		if synced {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	dirty := true
	a.ui.QueueUpdateDraw(func() { dirty = a.docs[id] == nil || a.docs[id].Dirty })
	if dirty {
		t.Fatal("editor autosave did not complete")
	}
	stop()
	stopped = true
	readBack, err := client.Read(ctx, id)
	if err != nil || !strings.Contains(readBack.Markdown, "First block — editor-live") {
		t.Fatalf("editor read-back: changed=%v err=%v", strings.Contains(readBack.Markdown, "First block — editor-live"), err)
	}
	if _, err := client.Save(ctx, id, readBack.Markdown, original.Markdown); err != nil {
		t.Fatal(err)
	}
	final, err := client.Read(ctx, id)
	if err != nil || final.Markdown != original.Markdown {
		t.Fatalf("editor restore verification: equal=%v err=%v", final.Markdown == original.Markdown, err)
	}
	restored = true
}

func TestSidebarDividerDragAndPageContextMenu(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	page := notion.Page{ID: "page", Title: "Page", Kind: "page", URL: "https://www.notion.so/page"}
	a := newApp(notion.NewDemo(nil), s, store.State{Pages: []notion.Page{page}}, nil, "", true)
	defer a.cancel()
	a.listed = []notion.Page{page}
	a.sidebarWidth, a.sidebarHeight = 30, 20
	a.rebuildList()

	divider, ok := a.root.GetItem(1).(*sidebarDivider)
	if !ok {
		t.Fatalf("divider type: %T", a.root.GetItem(1))
	}
	a.root.SetRect(0, 0, 100, 30)
	divider.SetRect(30, 0, 1, 30)
	h := divider.MouseHandler()
	if consumed, capture := h(tview.MouseLeftDown, tcell.NewEventMouse(30, 4, tcell.Button1, tcell.ModNone), func(tview.Primitive) {}); !consumed || capture != divider {
		t.Fatalf("divider did not capture drag: consumed=%v capture=%T", consumed, capture)
	}
	h(tview.MouseMove, tcell.NewEventMouse(42, 4, tcell.Button1, tcell.ModNone), func(tview.Primitive) {})
	h(tview.MouseLeftUp, tcell.NewEventMouse(42, 4, tcell.ButtonNone, tcell.ModNone), func(tview.Primitive) {})
	if a.state.SidebarWidth != 42 {
		t.Fatalf("dragged sidebar width: %d", a.state.SidebarWidth)
	}

	a.list.SetRect(0, 0, 30, 6)
	x, y, _, _ := a.list.GetInnerRect()
	a.list.MouseHandler()(tview.MouseRightClick, tcell.NewEventMouse(x+5, y, tcell.Button2, tcell.ModNone), func(p tview.Primitive) { a.ui.SetFocus(p) })
	if !a.modal {
		t.Fatal("right-click did not open the page context menu")
	}
	a.closeModal()
}

func TestAtMentionMenuInsertsTodayPeopleAndLiteralFallback(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	page := notion.Page{ID: "mentions", Title: "Mentions", Kind: "page"}
	content := notion.Content{Object: "page_markdown", ID: page.ID, Markdown: "Plan "}
	a := newApp(backendOnly{Backend: notion.NewDemo(nil)}, s, store.State{People: []notion.Person{{ID: "ada", Name: "Ada Lovelace"}}}, nil, "", true)
	defer a.cancel()
	a.docs[page.ID] = &notion.Doc{Page: page, Base: content, Text: content.Markdown, Fetched: time.Now()}
	a.open(page)
	a.editor.Select(len(a.editor.visible), len(a.editor.visible))
	a.editor.InputHandler()(tcell.NewEventKey(tcell.KeyRune, '@', tcell.ModNone), func(tview.Primitive) {})
	if a.mention == nil || !a.modal {
		t.Fatal("@ did not open the mention picker")
	}
	a.mention.input.SetText("today")
	if len(a.mention.items) != 1 || a.mention.items[0].label != "◷  Today" {
		t.Fatalf("today results: %+v", a.mention.items)
	}
	a.mention.items[0].run()
	today := time.Now().Format("2006-01-02")
	if !strings.Contains(a.editor.GetText(), `<mention-date start="`+today+`"/>`) || !strings.Contains(a.editor.visible, "@"+notionDate(today)) {
		t.Fatalf("today mention: source=%q visible=%q", a.editor.GetText(), a.editor.visible)
	}

	a.editor.Select(len(a.editor.visible), len(a.editor.visible))
	a.openMentionMenu()
	a.mention.input.SetText("ada")
	if len(a.mention.items) != 1 {
		t.Fatalf("people results: %+v", a.mention.items)
	}
	a.mention.items[0].run()
	if !strings.Contains(a.editor.GetText(), `<mention-user url="{{user://ada}}">Ada Lovelace</mention-user>`) || !strings.Contains(a.editor.visible, "@Ada Lovelace") {
		t.Fatalf("person mention: source=%q visible=%q", a.editor.GetText(), a.editor.visible)
	}

	a.editor.Select(len(a.editor.visible), len(a.editor.visible))
	a.openMentionMenu()
	a.mention.input.SetText("literal")
	a.closeModal()
	if !strings.HasSuffix(a.editor.GetText(), "@literal") {
		t.Fatalf("escaped mention query was lost: %q", a.editor.GetText())
	}
}

func TestPeopleAreLearnedFromExistingMentions(t *testing.T) {
	markdown := `Hello <mention-user url="{{user://person-a}}">Ada &amp; Co</mention-user>, ` +
		`again <mention-user url="{{user://person-a}}">Ada &amp; Co</mention-user>, ` +
		`and <mention-user url="{{user://person-b}}">Léa</mention-user>.`
	people := peopleFromMarkdown(markdown)
	if len(people) != 2 || people[0] != (notion.Person{ID: "person-a", Name: "Ada & Co"}) || people[1] != (notion.Person{ID: "person-b", Name: "Léa"}) {
		t.Fatalf("learned people: %+v", people)
	}
	ids := personIDsFromMarkdown(markdown + ` <mention-user url="user://person-c"/>`)
	if strings.Join(ids, ",") != "person-a,person-b,person-c" {
		t.Fatalf("mention identities: %v", ids)
	}
}

func TestDatabaseTableBoardFilteringAndSingleClickOpen(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rows := []notion.Page{
		withProperties(notion.Page{ID: "row-a", Title: "Alpha", Kind: "page", Edited: "2026-09-20"},
			notion.Property{Name: "Name", Type: "title", Text: "Alpha"}, notion.Property{Name: "Status", Type: "status", Text: "Doing", Values: []string{"Doing"}}, notion.Property{Name: "Owner", Type: "people", Text: "Ada", Values: []string{"Ada"}}),
		withProperties(notion.Page{ID: "row-b", Title: "Beta", Kind: "page", Edited: "2026-09-21"},
			notion.Property{Name: "Name", Type: "title", Text: "Beta"}, notion.Property{Name: "Status", Type: "status", Text: "Done", Values: []string{"Done"}}, notion.Property{Name: "Owner", Type: "people", Text: "Grace", Values: []string{"Grace"}}),
	}
	backend := &databaseRowsBackend{Backend: notion.NewDemo(nil), rows: rows}
	a := newApp(backend, s, store.State{}, nil, "", true)
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	a.ui.SetScreen(screen)
	screen.SetSize(130, 40)
	done := make(chan error, 1)
	go func() { done <- a.run() }()
	defer func() {
		a.ui.QueueUpdateDraw(func() { a.cancel(); a.ui.Stop() })
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	source := notion.Page{ID: "source", Title: "Tasks", Kind: "data_source"}
	a.ui.QueueUpdateDraw(func() { a.open(source) })
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ready := false
		a.ui.QueueUpdateDraw(func() { ready = a.database != nil && len(a.database.rows) == 2 && !a.database.loading })
		if ready {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	checkErr := ""
	a.ui.QueueUpdateDraw(func() {
		view := a.database
		if view == nil || view.table.GetCell(0, 0).Text != "Name" || view.table.GetCell(1, 0).Text != "Alpha" {
			checkErr = fmt.Sprintf("table view was not rendered: view=%+v", view)
			return
		}
		a.setDatabaseMode(view, "board")
		if view.group != "Status" || view.table.GetCell(0, 0).Text != "Doing  1" || view.table.GetCell(0, 1).Text != "Done  1" {
			checkErr = fmt.Sprintf("board grouping: group=%q first=%q second=%q", view.group, view.table.GetCell(0, 0).Text, view.table.GetCell(0, 1).Text)
			return
		}
		view.filter.SetText("Beta")
		if !strings.Contains(view.status.GetText(false), "1 of 2 rows") {
			checkErr = fmt.Sprintf("database filter status: %q", view.status.GetText(false))
			return
		}
		a.setDatabaseMode(view, "list")
		cell := view.table.GetCell(1, 0)
		if cell == nil || cell.Clicked == nil {
			checkErr = "database row is not mouse-clickable"
			return
		}
		cell.Clicked()
	})
	if checkErr != "" {
		t.Fatal(checkErr)
	}
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		opened := false
		a.ui.QueueUpdateDraw(func() { opened = a.database == nil && a.active == "row-b" && a.docs["row-b"] != nil })
		if opened {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("single-click database row did not open inside ntty")
}

func TestEllipsizePreservesGraphemesAndWidth(t *testing.T) {
	if got := ellipsize("Meeting 👩🏽‍💻 notes", 11); got != "Meeting 👩🏽‍💻…" || uniseg.StringWidth(got) > 11 {
		t.Fatalf("grapheme-safe ellipsis: %q (%d cells)", got, uniseg.StringWidth(got))
	}
}

func TestSidebarGroupsOpenedRecentsAndWorkspaceRoots(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	favorite := notion.Page{ID: "favorite", Title: "Pinned", Kind: "page", ParentKind: "page_id", ParentID: "parent"}
	recent := notion.Page{ID: "recent", Title: "Actually opened", Kind: "page", ParentKind: "page_id", ParentID: "parent"}
	root := notion.Page{ID: "root", Title: "Workspace root", Kind: "page", ParentKind: "workspace"}
	child := notion.Page{ID: "child", Title: "Nested result", Kind: "page", ParentKind: "page_id", ParentID: root.ID}
	globalDump := notion.Page{ID: "cached", Title: "Cached search page", Kind: "page"}
	a := newApp(notion.NewDemo(nil), s, store.State{Pages: []notion.Page{globalDump}, Pins: []notion.Page{favorite}, Recents: []notion.Page{recent}}, nil, "", false)
	defer a.cancel()
	a.listed = []notion.Page{root, child}
	a.rebuildList()
	if len(a.favoritePages) != 1 || a.favoritePages[0].ID != favorite.ID || len(a.recentPages) != 1 || a.recentPages[0].ID != recent.ID {
		t.Fatalf("sidebar groups are not backed by pins/opens: favorites=%+v recents=%+v", a.favoritePages, a.recentPages)
	}
	if len(a.visible) != 1 || a.visible[0].ID != root.ID {
		t.Fatalf("workspace contains non-roots or cached search dump: %+v", a.visible)
	}
	a.query = "nested"
	a.rebuildList()
	if len(a.visible) != 2 || a.workspaceLabel.GetText(false) != "  Search results" {
		t.Fatalf("search results were presented as workspace roots: label=%q pages=%+v", a.workspaceLabel.GetText(false), a.visible)
	}

	a.query = ""
	a.docs[root.ID] = &notion.Doc{Page: root, Base: notion.Content{Object: "page_markdown", ID: root.ID}}
	a.open(root)
	if len(a.state.Recents) == 0 || a.state.Recents[0].ID != root.ID {
		t.Fatalf("opening did not update recents: %+v", a.state.Recents)
	}
}

func TestCollectWorkspaceFollowsEveryPageAndKeepsOnlyRoots(t *testing.T) {
	rootA := notion.Page{ID: "root-a", Title: "A", ParentKind: "workspace"}
	rootB := notion.Page{ID: "root-b", Title: "B", ParentKind: "workspace"}
	child := notion.Page{ID: "child", Title: "Nested", ParentKind: "page_id"}
	b := &pagedSearch{Backend: notion.NewDemo(nil), pages: map[string]notion.Listing{
		"":     {Pages: []notion.Page{rootA, child}, Cursor: "next"},
		"next": {Pages: []notion.Page{rootA, rootB}},
	}}
	var progress [][]notion.Page
	roots, scanned, cursor, err := collectWorkspace(context.Background(), b, func(pages []notion.Page, _ string, _ int) {
		progress = append(progress, pages)
	})
	if err != nil || scanned != 4 || cursor != "" || len(roots) != 2 || roots[0].ID != rootA.ID || roots[1].ID != rootB.ID {
		t.Fatalf("workspace index: roots=%+v scanned=%d cursor=%q err=%v", roots, scanned, cursor, err)
	}
	if fmt.Sprint(b.seen) != "[ next]" || len(progress) != 2 || len(progress[0]) != 1 || len(progress[1]) != 2 {
		t.Fatalf("workspace pagination: cursors=%q progress=%+v", b.seen, progress)
	}

	b.pages["next"] = notion.Listing{Cursor: "next"}
	if _, _, _, err := collectWorkspace(context.Background(), b, nil); err == nil {
		t.Fatal("repeated workspace cursor was accepted")
	}
}

func TestPaletteWheelAndDimBackdropRestore(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pages := make([]notion.Page, 40)
	for i := range pages {
		pages[i] = notion.Page{ID: fmt.Sprintf("p-%02d", i), Title: fmt.Sprintf("Page %02d", i), Kind: "page", ParentKind: "workspace"}
	}
	a := newApp(notion.NewDemo(nil), s, store.State{Pages: pages}, nil, "", true)
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	a.ui.SetScreen(screen)
	screen.SetSize(100, 30)
	done := make(chan error, 1)
	go func() { done <- a.run() }()
	defer func() {
		a.ui.QueueUpdate(func() { a.cancel(); a.ui.Stop() })
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	wait := func(check func() bool) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			ok := false
			a.ui.QueueUpdate(func() { ok = check() })
			if ok {
				return
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatal("UI did not reach expected state")
	}
	wait(func() bool { _, _, width, _ := a.title.GetRect(); return width > 0 })
	var titleX, titleY int
	a.ui.QueueUpdate(func() { titleX, titleY, _, _ = a.title.GetInnerRect() })
	a.ui.QueueEvent(tcell.NewEventKey(tcell.KeyCtrlK, 0, tcell.ModNone))
	wait(func() bool { return a.palette != nil })
	a.ui.QueueUpdateDraw(func() { a.palette.input.SetText("page") })
	wait(func() bool { return a.palette.list.GetItemCount() >= 40 })
	a.ui.QueueUpdate(func() {
		_, _, style, _ := screen.GetContent(titleX, titleY)
		_, _, attrs := style.Decompose()
		if attrs&tcell.AttrDim == 0 {
			t.Error("modal did not dim the background")
		}
		x, y, _, _ := a.palette.input.GetInnerRect()
		_, _, style, _ = screen.GetContent(x, y)
		_, _, attrs = style.Decompose()
		if attrs&tcell.AttrDim != 0 {
			t.Error("palette foreground was dimmed with the background")
		}
	})
	var listX, listY int
	a.ui.QueueUpdate(func() { listX, listY, _, _ = a.palette.list.GetInnerRect() })
	a.ui.QueueUpdate(func() {
		event := tcell.NewEventMouse(listX+1, listY+1, tcell.WheelDown, tcell.ModNone)
		a.ui.GetMouseCapture()(event, tview.MouseScrollDown)
	})
	wait(func() bool { offset, _ := a.palette.list.GetOffset(); return offset > 0 })
	a.ui.QueueUpdate(func() {
		if a.ui.GetFocus() != a.palette.input {
			t.Error("wheel scrolling stole palette query focus")
		}
	})
	a.ui.QueueEvent(tcell.NewEventKey(tcell.KeyCtrlK, 0, tcell.ModNone))
	wait(func() bool { return a.palette == nil })
	a.ui.QueueUpdate(func() {
		_, _, style, _ := screen.GetContent(titleX, titleY)
		_, _, attrs := style.Decompose()
		if attrs&tcell.AttrDim != 0 {
			t.Error("closing the palette left the page dimmed")
		}
	})
	a.ui.QueueEvent(tcell.NewEventKey(tcell.KeyRune, 'k', tcell.ModMeta))
	wait(func() bool { return a.palette != nil })
	a.ui.QueueEvent(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone))
	wait(func() bool { return a.palette == nil })
}
