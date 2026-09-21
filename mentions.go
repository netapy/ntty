package main

import (
	"context"
	"html"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"ntty/internal/notion"
)

type mentionMenu struct {
	input            *tview.InputField
	list             *tview.List
	items            []menuItem
	people           []notion.Person
	pos              int
	cancel           context.CancelFunc
	loading          bool
	pages            []notion.Page
	searchCancel     context.CancelFunc
	searchGeneration int
}

type dateMention struct {
	label, token, raw string
}

func dateMentions(now time.Time) []dateMention {
	day := func(offset int) string { return now.AddDate(0, 0, offset).Format("2006-01-02") }
	return []dateMention{
		{"Today", "today", `<mention-date start="` + day(0) + `"/>`},
		{"Tomorrow", "tomorrow", `<mention-date start="` + day(1) + `"/>`},
		{"Yesterday", "yesterday", `<mention-date start="` + day(-1) + `"/>`},
		{"Next week", "next week", `<mention-date start="` + day(7) + `"/>`},
	}
}

func mergePeople(first, second []notion.Person) []notion.Person {
	seen := map[string]bool{}
	result := make([]notion.Person, 0, len(first)+len(second))
	for _, set := range [][]notion.Person{first, second} {
		for _, person := range set {
			if person.ID == "" || strings.TrimSpace(person.Name) == "" || seen[person.ID] {
				continue
			}
			seen[person.ID] = true
			result = append(result, person)
		}
	}
	return result
}

func (a *app) rememberPeople(people ...notion.Person) {
	merged := mergePeople(a.state.People, people)
	if len(merged) == len(a.state.People) {
		return
	}
	a.state.People = merged
	a.persistState()
}

func peopleFromPages(pages []notion.Page) []notion.Person {
	var people []notion.Person
	for _, page := range pages {
		for _, property := range page.PropertyValues() {
			people = append(people, property.People...)
		}
	}
	return mergePeople(people, nil)
}

func peopleFromMarkdown(markdown string) []notion.Person {
	var people []notion.Person
	for offset := 0; ; {
		start := strings.Index(markdown[offset:], "<mention-user")
		if start < 0 {
			break
		}
		start += offset
		raw, label, _ := notionInline(markdown[start:])
		if raw == "" {
			offset = start + len("<mention-user")
			continue
		}
		id, name := notionUserID(raw), strings.TrimPrefix(label, "@")
		if id != "" && name != "" && name != "Person" {
			people = append(people, notion.Person{ID: id, Name: name})
		}
		offset = start + len(raw)
	}
	return mergePeople(people, nil)
}

func personIDsFromMarkdown(markdown string) []string {
	seen := map[string]bool{}
	var ids []string
	for offset := 0; ; {
		start := strings.Index(markdown[offset:], "<mention-user")
		if start < 0 {
			break
		}
		start += offset
		raw, _, _ := notionInline(markdown[start:])
		if raw == "" {
			offset = start + len("<mention-user")
			continue
		}
		if id := notionUserID(raw); id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
		offset = start + len(raw)
		if len(ids) == 100 {
			break
		}
	}
	return ids
}

func (a *app) openMentionMenu() {
	if a.modal || a.editor.GetDisabled() {
		return
	}
	_, pos, _ := a.editor.GetSelection()
	m := &mentionMenu{
		input:  tview.NewInputField().SetLabel("@ ").SetPlaceholder("Date, person, or page…"),
		list:   tview.NewList().ShowSecondaryText(false).SetHighlightFullLine(false).SetMainTextStyle(tcell.StyleDefault).SetSelectedStyle(navigationSelection),
		people: append([]notion.Person(nil), a.state.People...),
		pos:    pos,
	}
	ctx, cancel := context.WithCancel(a.ctx)
	m.cancel = cancel
	mentionedIDs := personIDsFromMarkdown(a.editor.GetText())
	m.input.SetFieldStyle(tcell.StyleDefault).SetPlaceholderStyle(quiet)
	a.mention = m
	choose := func(raw string) {
		if m.cancel != nil {
			m.cancel()
		}
		a.mention = nil
		a.closeModal()
		a.editor.Select(pos, pos)
		if !a.editor.InsertAtom(raw) {
			a.message("That mention cannot be inserted safely here", true)
		}
		a.ui.SetFocus(a.editor)
	}
	filter := func() {
		query := strings.TrimSpace(strings.ToLower(m.input.GetText()))
		selected := ""
		if index := m.list.GetCurrentItem(); index >= 0 && index < len(m.items) {
			selected = m.items[index].label
		}
		m.items = nil
		m.list.Clear()
		add := func(label, raw string) {
			m.items = append(m.items, menuItem{label, func() { choose(raw) }})
			m.list.AddItem(tview.Escape(label), "", 0, nil)
		}
		for _, date := range dateMentions(time.Now()) {
			if query == "" || strings.Contains(strings.ToLower(date.label), query) || strings.Contains(date.token, query) {
				add("◷  "+date.label, date.raw)
			}
		}
		if len(query) == 10 {
			if parsed, err := time.Parse("2006-01-02", query); err == nil {
				add("◷  "+parsed.Format("2 Jan 2006"), `<mention-date start="`+query+`"/>`)
			}
		}
		for _, person := range m.people {
			if query != "" && !strings.Contains(strings.ToLower(person.Name), query) {
				continue
			}
			raw := `<mention-user url="{{user://` + html.EscapeString(person.ID) + `}}">` + html.EscapeString(person.Name) + `</mention-user>`
			add("@  "+person.Name, raw)
		}
		pages := mergePages(m.pages, mergePages(a.state.Recents, a.state.Pages))
		count := 0
		for _, page := range pages {
			if page.Kind != "page" || !matches(page.Title, query) {
				continue
			}
			raw := `<mention-page url="https://www.notion.so/` + html.EscapeString(page.ID) + `">` + html.EscapeString(page.Title) + `</mention-page>`
			add("↗  "+page.Title, raw)
			count++
			if count >= 30 {
				break
			}
		}
		for i, item := range m.items {
			if item.label == selected {
				m.list.SetCurrentItem(i)
				break
			}
		}
		if m.loading {
			m.list.AddItem("Finding people…", "", 0, nil)
		} else if len(m.items) == 0 {
			m.list.AddItem("No matching dates, people, or pages", "", 0, nil)
		}
	}
	activate := func() {
		i := m.list.GetCurrentItem()
		if i >= 0 && i < len(m.items) {
			m.items[i].run()
		}
	}
	m.input.SetChangedFunc(func(query string) {
		if m.searchCancel != nil {
			m.searchCancel()
		}
		m.searchGeneration++
		generation := m.searchGeneration
		m.pages = nil
		filter()
		query = strings.TrimSpace(query)
		if query == "" || a.demo {
			return
		}
		searchCtx, stop := context.WithCancel(ctx)
		m.searchCancel = stop
		go func() {
			timer := time.NewTimer(300 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-searchCtx.Done():
				return
			case <-timer.C:
			}
			result, err := a.backend.Search(searchCtx, query, "")
			if searchCtx.Err() != nil {
				return
			}
			a.ui.QueueUpdateDraw(func() {
				if a.mention != m || generation != m.searchGeneration || err != nil {
					return
				}
				m.pages = result.Pages
				filter()
			})
		}()
	})
	m.list.SetSelectedFunc(func(int, string, string, rune) { activate() })
	pane := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(m.input, 1, 0, true).AddItem(m.list, 0, 1, false)
	pane.Box = tview.NewBox()
	pane.SetBorder(true).SetBorderStyle(quiet).SetTitle(" Mention ")
	pane.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyUp, tcell.KeyDown, tcell.KeyPgUp, tcell.KeyPgDn:
			m.list.InputHandler()(event, func(tview.Primitive) {})
			return nil
		case tcell.KeyEnter:
			activate()
			return nil
		}
		return event
	})
	filter()
	_, _, screenWidth, screenHeight := a.layers.GetRect()
	if screenWidth < 42 || screenHeight < 18 {
		a.overlay(pane, 38, 15)
	} else {
		row, column := a.editor.position(pos)
		x, y, _, _ := a.editor.GetInnerRect()
		offset, _ := a.editor.GetOffset()
		left := min(max(0, x+column), screenWidth-38)
		top := min(max(0, y+row-offset+1), screenHeight-15)
		line := tview.NewFlex().AddItem(nil, left, 0, false).AddItem(pane, 38, 0, true).AddItem(nil, 0, 1, false)
		overlay := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(nil, top, 0, false).AddItem(line, 15, 0, true).AddItem(nil, 0, 1, false)
		dimModalBackground(overlay)
		overlay.SetMouseCapture(func(action tview.MouseAction, event *tcell.EventMouse) (tview.MouseAction, *tcell.EventMouse) {
			if action == tview.MouseLeftDown && !pane.InRect(event.Position()) {
				a.closeModal()
				return tview.MouseConsumed, nil
			}
			return action, event
		})
		a.modal, a.returnFocus = true, a.ui.GetFocus()
		a.layers.AddPage("modal", overlay, true, true)
	}
	a.ui.SetFocus(m.input)

	if backend, ok := a.backend.(notion.PeopleBackend); ok {
		m.loading = true
		filter()
		go func() {
			cursor := ""
			people := append([]notion.Person(nil), m.people...)
			for {
				listing, err := backend.People(ctx, cursor)
				if err != nil {
					a.ui.QueueUpdateDraw(func() {
						if a.mention == m {
							m.loading = false
							filter()
							a.message("People: "+err.Error(), true)
						}
					})
					return
				}
				people = mergePeople(people, listing.People)
				if listing.Cursor == "" {
					break
				}
				cursor = listing.Cursor
			}
			if resolver, ok := a.backend.(notion.PersonBackend); ok {
				known := map[string]bool{}
				for _, person := range people {
					known[person.ID] = true
				}
				for _, id := range mentionedIDs {
					if known[id] {
						continue
					}
					person, resolveErr := resolver.Person(ctx, id)
					if resolveErr == nil {
						people = mergePeople(people, []notion.Person{person})
						known[id] = true
					}
				}
			}
			a.ui.QueueUpdateDraw(func() {
				if a.mention != m {
					return
				}
				m.people, m.loading = people, false
				a.state.People = mergePeople(people, a.state.People)
				a.persistState()
				filter()
			})
		}()
	}
}
