package main

import (
	"context"
	"sort"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"ntty/internal/notion"
)

type databaseCell struct{ row, column int }

type databasePropertyCache struct {
	raw    string
	values []notion.Property
}

type databaseView struct {
	source                  notion.Page
	pane                    *tview.Flex
	table                   *tview.Table
	filter                  *tview.InputField
	status                  *tview.TextView
	groupButton, sortButton *tview.Button
	modeButtons             map[string]*tview.Button
	rows                    []notion.Page
	cells                   map[databaseCell]notion.Page
	cursor                  string
	mode, group, order      string
	loading                 bool
	generation              int
	cancel                  context.CancelFunc
	native                  *tview.DropDown
	views                   []notion.DatabaseView
	details                 map[string]notion.DatabaseView
	viewID, queryID         string
	incomplete              bool
	loaded, cacheMetadata   bool
	properties              map[string]databasePropertyCache
}

var databaseModes = []string{"table", "board", "list", "gallery"}

func titleCase(value string) string {
	if value == "" {
		return value
	}
	return strings.ToUpper(value[:1]) + value[1:]
}

func (a *app) openDatabaseSource(source notion.Page) {
	if a.modal {
		return
	}
	mode := "table"
	if saved := a.state.DatabaseViews[source.ID]; saved != "" {
		for _, candidate := range databaseModes {
			if candidate == saved {
				mode = saved
			}
		}
	}
	view := &databaseView{
		source:      source,
		table:       tview.NewTable().SetSelectable(true, false).SetFixed(1, 1).SetSelectedStyle(navigationSelection),
		filter:      tview.NewInputField().SetLabel("Find loaded rows  ").SetPlaceholder("Text, or Property: value"),
		status:      tview.NewTextView().SetTextStyle(quiet).SetWrap(false),
		modeButtons: map[string]*tview.Button{}, cells: map[databaseCell]notion.Page{},
		properties: map[string]databasePropertyCache{},
		mode:       mode, group: a.state.DatabaseGroups[source.ID], order: a.state.DatabaseSorts[source.ID],
	}
	if view.order == "" {
		view.order = "title-asc"
	}
	view.filter.SetFieldStyle(tcell.StyleDefault).SetPlaceholderStyle(quiet)
	view.filter.SetChangedFunc(func(string) { a.renderDatabase(view) })
	view.table.SetSelectedFunc(func(row, column int) {
		if page, ok := view.cells[databaseCell{row, column}]; ok {
			a.openDatabaseRow(view, page)
		}
	})
	view.table.SetMouseCapture(func(action tview.MouseAction, event *tcell.EventMouse) (tview.MouseAction, *tcell.EventMouse) {
		if action == tview.MouseRightClick {
			row, column := view.table.CellAt(event.Position())
			if page, ok := view.cells[databaseCell{row, column}]; ok {
				a.closeModal()
				a.openPageMenu(page)
				return tview.MouseConsumed, nil
			}
		}
		return action, event
	})
	modeBar := tview.NewFlex()
	for _, modeName := range databaseModes {
		name := modeName
		button := a.button(titleCase(name), func() { a.setDatabaseMode(view, name) })
		view.modeButtons[name] = button
		modeBar.AddItem(button, 0, 1, false)
	}
	view.groupButton = a.button("Group", func() { a.cycleDatabaseGroup(view) })
	view.sortButton = a.button("Sort", func() { a.cycleDatabaseSort(view) })
	modeBar.AddItem(view.groupButton, 0, 1, false).
		AddItem(view.sortButton, 0, 1, false).
		AddItem(a.button("Refresh", func() { a.loadDatabase(view, true) }), 0, 1, false).
		AddItem(a.button("More", func() { a.loadDatabase(view, false) }), 0, 1, false).
		AddItem(a.button("Close", a.closeModal), 0, 1, false)
	view.pane = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(modeBar, 1, 0, false).
		AddItem(view.filter, 1, 0, false).
		AddItem(view.table, 0, 1, true).
		AddItem(view.status, 1, 0, false)
	if _, ok := a.backend.(notion.ViewBackend); ok {
		view.native = tview.NewDropDown().SetLabel("Notion view  ").SetOptions([]string{"All rows · local layout"}, nil).SetCurrentOption(0)
		view.pane.AddItem(view.native, 1, 0, false)
	}
	view.pane.Box = tview.NewBox()
	view.pane.SetBorder(true).SetBorderStyle(quiet).SetTitle(" Database · " + tview.Escape(source.Title) + " · read-only ")
	view.pane.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if a.ui.GetFocus() != view.filter && a.ui.GetFocus() != view.native && event.Key() == tcell.KeyRune {
			switch event.Rune() {
			case '/':
				a.ui.SetFocus(view.filter)
				return nil
			case 'v':
				a.cycleDatabaseMode(view)
				return nil
			case 'n':
				if view.native != nil {
					a.ui.SetFocus(view.native)
				}
				return nil
			case 'g':
				a.cycleDatabaseGroup(view)
				return nil
			case 's':
				a.cycleDatabaseSort(view)
				return nil
			case 'r':
				a.loadDatabase(view, true)
				return nil
			case 'm':
				a.loadDatabase(view, false)
				return nil
			case '1', '2', '3', '4':
				a.setDatabaseMode(view, databaseModes[int(event.Rune()-'1')])
				return nil
			}
		}
		if event.Key() == tcell.KeyTab || event.Key() == tcell.KeyBacktab {
			if a.ui.GetFocus() == view.filter {
				a.ui.SetFocus(view.table)
			} else if a.ui.GetFocus() == view.table && view.native != nil {
				a.ui.SetFocus(view.native)
			} else {
				a.ui.SetFocus(view.filter)
			}
			return nil
		}
		return event
	})
	a.database = view
	a.overlay(view.pane, 124, 38)
	a.ui.SetFocus(view.table)
	a.renderDatabase(view)
	a.loadDatabase(view, true)
	if view.native != nil {
		a.loadDatabaseViews(view)
	}
}

func (a *app) setDatabaseMode(view *databaseView, mode string) {
	if a.database != view {
		return
	}
	view.mode = mode
	if a.state.DatabaseViews == nil {
		a.state.DatabaseViews = map[string]string{}
	}
	a.state.DatabaseViews[view.source.ID] = mode
	a.persistState()
	a.renderDatabase(view)
}

func (a *app) cycleDatabaseMode(view *databaseView) {
	for index, mode := range databaseModes {
		if mode == view.mode {
			a.setDatabaseMode(view, databaseModes[(index+1)%len(databaseModes)])
			return
		}
	}
	a.setDatabaseMode(view, "table")
}

func (view *databaseView) propertyValues(page notion.Page) []notion.Property {
	if cached, ok := view.properties[page.ID]; ok && cached.raw == page.PropertyData {
		return cached.values
	}
	values := page.PropertyValues()
	if view.properties == nil {
		view.properties = map[string]databasePropertyCache{}
	}
	view.properties[page.ID] = databasePropertyCache{raw: page.PropertyData, values: values}
	return values
}

func databaseGroupProperties(view *databaseView) []string {
	seen := map[string]bool{}
	priority := map[string]int{}
	var result []string
	for _, page := range view.rows {
		for _, property := range view.propertyValues(page) {
			switch property.Type {
			case "status", "select", "multi_select", "checkbox", "people", "created_by", "last_edited_by":
				if !seen[property.Name] {
					seen[property.Name] = true
					result = append(result, property.Name)
					priority[property.Name] = map[string]int{"status": 0, "select": 1, "multi_select": 2, "checkbox": 3, "people": 4, "created_by": 5, "last_edited_by": 6}[property.Type]
				}
			}
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if priority[result[i]] != priority[result[j]] {
			return priority[result[i]] < priority[result[j]]
		}
		return result[i] < result[j]
	})
	return result
}

func (a *app) cycleDatabaseGroup(view *databaseView) {
	properties := databaseGroupProperties(view)
	if len(properties) == 0 {
		view.group = ""
		a.message("This database has no groupable properties", false)
		a.renderDatabase(view)
		return
	}
	next := 0
	for index, property := range properties {
		if property == view.group {
			next = (index + 1) % len(properties)
			break
		}
	}
	view.group = properties[next]
	if a.state.DatabaseGroups == nil {
		a.state.DatabaseGroups = map[string]string{}
	}
	a.state.DatabaseGroups[view.source.ID] = view.group
	a.persistState()
	a.renderDatabase(view)
}

func (a *app) cycleDatabaseSort(view *databaseView) {
	switch view.order {
	case "title-asc":
		view.order = "title-desc"
	case "title-desc":
		view.order = "edited-desc"
	case "edited-desc":
		view.order = "title-asc"
		if view.viewID != "" {
			view.order = "native"
		}
	default:
		view.order = "title-asc"
	}
	if a.state.DatabaseSorts == nil {
		a.state.DatabaseSorts = map[string]string{}
	}
	if view.order != "native" {
		a.state.DatabaseSorts[view.source.ID] = view.order
	}
	a.persistState()
	a.renderDatabase(view)
}

func propertyValue(properties []notion.Property, name string) notion.Property {
	for _, property := range properties {
		if property.Name == name {
			return property
		}
	}
	return notion.Property{Name: name}
}

func databaseProperties(view *databaseView) []string {
	seen := map[string]bool{}
	title := "Name"
	for _, page := range view.rows {
		for _, property := range view.propertyValues(page) {
			if property.Type == "title" {
				title = property.Name
				break
			}
		}
		if title != "Name" {
			break
		}
	}
	result := []string{title}
	seen[title] = true
	for _, page := range view.rows {
		for _, property := range view.propertyValues(page) {
			if property.Type == "title" {
				continue
			}
			if !seen[property.Name] {
				seen[property.Name] = true
				result = append(result, property.Name)
			}
			if len(result) >= 7 {
				return result
			}
		}
	}
	return result
}

func databaseMetadata(view *databaseView, page notion.Page, omit string) string {
	var values []string
	for _, property := range view.propertyValues(page) {
		if property.Type == "title" || property.Name == omit || property.Text == "" {
			continue
		}
		values = append(values, property.Name+": "+property.Text)
		if len(values) == 2 {
			break
		}
	}
	return strings.Join(values, " · ")
}

func (a *app) databaseCell(view *databaseView, row, column int, text string, page notion.Page) *tview.TableCell {
	cell := tview.NewTableCell(tview.Escape(text)).SetTextColor(tcell.ColorDefault).SetBackgroundColor(tcell.ColorDefault)
	if page.ID != "" {
		cell.SetSelectable(true).SetClickedFunc(func() bool { a.openDatabaseRow(view, page); return true })
		view.cells[databaseCell{row, column}] = page
	} else {
		cell.SetSelectable(false).SetTextColor(tcell.ColorTeal).SetAttributes(tcell.AttrBold)
	}
	return cell
}

func (a *app) renderDatabase(view *databaseView) {
	if a.database != view {
		return
	}
	for name, button := range view.modeButtons {
		label := titleCase(name)
		if name == view.mode {
			label = "[" + label + "]"
		}
		button.SetLabel(label)
	}
	groupLabel := "Group"
	if view.group != "" {
		groupLabel += ": " + view.group
	}
	view.groupButton.SetLabel(groupLabel)
	view.sortButton.SetLabel("Sort: " + strings.ReplaceAll(view.order, "-", " "))
	query := strings.ToLower(strings.TrimSpace(view.filter.GetText()))
	rows := make([]notion.Page, 0, len(view.rows))
	for _, page := range view.rows {
		if databaseMatchesProperties(page.Title, view.propertyValues(page), query) {
			rows = append(rows, page)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if view.order == "native" {
			return false
		}
		if view.order == "edited-desc" {
			return rows[i].Edited > rows[j].Edited
		}
		left, right := strings.ToLower(rows[i].Title), strings.ToLower(rows[j].Title)
		if view.order == "title-desc" {
			return left > right
		}
		return left < right
	})
	view.table.SetSelectable(true, view.mode == "board" || view.mode == "gallery")
	view.table.Clear()
	view.cells = map[databaseCell]notion.Page{}
	switch view.mode {
	case "board":
		a.renderDatabaseBoard(view, rows)
	case "list":
		a.renderDatabaseList(view, rows)
	case "gallery":
		a.renderDatabaseGallery(view, rows)
	default:
		a.renderDatabaseTable(view, rows)
	}
	loading := ""
	if view.loading {
		loading = " · loading…"
	} else if view.cursor != "" {
		loading = " · more available"
	}
	if view.incomplete {
		loading += " · Notion result limit reached"
	}
	if view.loaded && view.cacheMetadata {
		loading += " · cached metadata (r refreshes)"
	}
	view.status.SetText("  " + titleCase(view.mode) + " · " + itoa(len(rows)) + " of " + itoa(len(view.rows)) + " rows loaded" + loading + " · / find · n Notion views · v layout · g group · s sort")
}

func (a *app) renderDatabaseTable(view *databaseView, rows []notion.Page) {
	properties := databaseProperties(view)
	for column, property := range properties {
		view.table.SetCell(0, column, a.databaseCell(view, 0, column, property, notion.Page{}).SetExpansion(1))
	}
	for row, page := range rows {
		values := view.propertyValues(page)
		for column, property := range properties {
			text := page.Title
			if column > 0 {
				text = propertyValue(values, property).Text
			}
			view.table.SetCell(row+1, column, a.databaseCell(view, row+1, column, text, page).SetExpansion(1))
		}
	}
}

func groupValues(view *databaseView, page notion.Page, property string) []string {
	value := propertyValue(view.propertyValues(page), property)
	if len(value.Values) > 0 {
		return value.Values
	}
	if value.Text != "" {
		return []string{value.Text}
	}
	return []string{"No " + property}
}

func (a *app) renderDatabaseBoard(view *databaseView, rows []notion.Page) {
	properties := databaseGroupProperties(view)
	validGroup := false
	for _, property := range properties {
		if property == view.group {
			validGroup = true
			break
		}
	}
	if !validGroup {
		view.group = ""
		if len(properties) > 0 {
			view.group = properties[0]
		}
	}
	if view.group == "" {
		a.renderDatabaseList(view, rows)
		return
	}
	groups, order := map[string][]notion.Page{}, []string{}
	for _, page := range rows {
		for _, group := range groupValues(view, page, view.group) {
			if _, ok := groups[group]; !ok {
				order = append(order, group)
			}
			groups[group] = append(groups[group], page)
		}
	}
	sort.Strings(order)
	for column, group := range order {
		view.table.SetCell(0, column, a.databaseCell(view, 0, column, group+"  "+itoa(len(groups[group])), notion.Page{}).SetExpansion(1))
		for row, page := range groups[group] {
			text := page.Title
			if meta := databaseMetadata(view, page, view.group); meta != "" {
				text += " · " + meta
			}
			view.table.SetCell(row+1, column, a.databaseCell(view, row+1, column, text, page).SetExpansion(1))
		}
	}
}

func (a *app) renderDatabaseList(view *databaseView, rows []notion.Page) {
	view.table.SetCell(0, 0, a.databaseCell(view, 0, 0, "Name", notion.Page{}).SetExpansion(1))
	for row, page := range rows {
		text := page.Title
		if meta := databaseMetadata(view, page, ""); meta != "" {
			text += "  ·  " + meta
		}
		view.table.SetCell(row+1, 0, a.databaseCell(view, row+1, 0, text, page).SetExpansion(1))
	}
}

func (a *app) renderDatabaseGallery(view *databaseView, rows []notion.Page) {
	const columns = 3
	for column := 0; column < columns; column++ {
		view.table.SetCell(0, column, a.databaseCell(view, 0, column, "", notion.Page{}).SetExpansion(1))
	}
	for index, page := range rows {
		row, column := index/columns+1, index%columns
		text := "◇ " + page.Title
		if meta := databaseMetadata(view, page, ""); meta != "" {
			text += " · " + meta
		}
		view.table.SetCell(row, column, a.databaseCell(view, row, column, text, page).SetExpansion(1))
	}
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := [20]byte{}
	index := len(digits)
	for value > 0 {
		index--
		digits[index] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[index:])
}

func (a *app) openDatabaseRow(view *databaseView, page notion.Page) {
	if a.database != view {
		return
	}
	a.closeModal()
	a.state.Pages = mergePages([]notion.Page{page}, a.state.Pages)
	a.persistState()
	a.open(page)
}

func (a *app) loadDatabase(view *databaseView, reset bool) {
	if a.database != view || view.loading && !reset || !reset && view.cursor == "" {
		return
	}
	if view.cancel != nil {
		view.cancel()
	}
	ctx, cancel := context.WithCancel(a.ctx)
	view.cancel, view.loading = cancel, true
	view.generation++
	generation := view.generation
	cursor := view.cursor
	viewID, queryID := view.viewID, view.queryID
	cachedBackend, canCache := a.backend.(notion.CachedViewBackend)
	if reset {
		cursor = ""
		queryID = ""
		view.cacheMetadata = viewID != "" && !view.loaded && canCache
	}
	cacheMetadata := view.cacheMetadata
	var cachedPages []notion.Page
	if cacheMetadata {
		cachedPages = append([]notion.Page(nil), a.state.Pages...)
	}
	a.renderDatabase(view)
	go func() {
		var listing notion.Listing
		var err error
		var native notion.ViewListing
		if viewID != "" {
			if cacheMetadata {
				native, err = cachedBackend.QueryViewCached(ctx, viewID, queryID, cursor, cachedPages)
			} else {
				native, err = a.backend.(notion.ViewBackend).QueryView(ctx, viewID, queryID, cursor)
			}
			listing = native.Listing
		} else {
			listing, err = a.backend.Query(ctx, view.source.ID, cursor)
		}
		a.ui.QueueUpdateDraw(func() {
			if a.database != view || generation != view.generation {
				return
			}
			view.loading = false
			if err != nil {
				a.message("Database: "+err.Error(), true)
				a.renderDatabase(view)
				return
			}
			if reset {
				view.rows = listing.Pages
				view.properties = map[string]databasePropertyCache{}
			} else {
				view.rows = mergePages(view.rows, listing.Pages)
			}
			view.loaded = true
			view.cursor = listing.Cursor
			view.queryID, view.incomplete = native.QueryID, native.Incomplete
			a.state.Pages = mergePages(listing.Pages, a.state.Pages)
			a.persistState()
			a.renderDatabase(view)
		})
	}()
}

func databaseMatches(page notion.Page, query string) bool {
	return databaseMatchesProperties(page.Title, page.PropertyValues(), query)
}

func databaseMatchesProperties(title string, properties []notion.Property, query string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return true
	}
	name, value, scoped := strings.Cut(query, ":")
	var values []string
	if !scoped {
		values = append(values, title)
	}
	for _, property := range properties {
		if !scoped || strings.EqualFold(property.Name, strings.TrimSpace(name)) {
			values = append(values, property.Text)
		}
	}
	if scoped {
		query = strings.TrimSpace(value)
	}
	return len(values) > 0 && strings.Contains(strings.ToLower(strings.Join(values, " ")), query)
}

func (a *app) configureDatabaseNative(view *databaseView, native notion.DatabaseView) {
	view.mode = "table"
	for _, mode := range databaseModes {
		if mode == native.Type {
			view.mode = mode
		}
	}
	view.group = native.Configuration.GroupBy.PropertyName
	if view.mode != native.Type {
		a.message(native.Type+" view: rows shown as a table; native filters and sorts retained", false)
	}
}

func (a *app) loadDatabaseViews(view *databaseView) {
	go func() {
		views, err := a.backend.(notion.ViewBackend).Views(a.ctx, view.source.ID)
		a.ui.QueueUpdateDraw(func() {
			if a.database != view {
				return
			}
			if err != nil {
				a.message("Notion views unavailable: "+err.Error(), true)
				return
			}
			view.views = views
			labels := []string{"All rows · local layout"}
			for _, native := range views {
				labels = append(labels, native.Name+" · "+native.Type)
			}
			view.native.SetOptions(labels, func(_ string, index int) {
				if index > 0 && view.views[index-1].Type == "dashboard" {
					a.message("Dashboard widgets are not rendered in the terminal", false)
					return
				}
				view.viewID, view.queryID, view.cursor = "", "", ""
				view.rows = nil
				view.incomplete = false
				view.loaded = false
				view.order = "title-asc"
				if index > 0 {
					native := view.views[index-1]
					cachedDetail, hasDetail := view.details[native.ID]
					if hasDetail {
						native = cachedDetail
					}
					view.viewID, view.order = native.ID, "native"
					a.configureDatabaseNative(view, native)
					if details, ok := a.backend.(notion.ViewDetailBackend); ok && !hasDetail {
						if view.cancel != nil {
							view.cancel()
						}
						view.generation++
						generation := view.generation
						ctx, cancel := context.WithCancel(a.ctx)
						view.cancel = cancel
						view.loading = true
						a.renderDatabase(view)
						go func(id string) {
							detail, err := details.View(ctx, id)
							a.ui.QueueUpdateDraw(func() {
								if a.database != view || view.generation != generation || ctx.Err() != nil {
									return
								}
								view.loading = false
								if err != nil {
									a.message("Notion view details unavailable: "+err.Error(), true)
								} else {
									if view.details == nil {
										view.details = map[string]notion.DatabaseView{}
									}
									view.details[id] = detail
									a.configureDatabaseNative(view, detail)
								}
								a.loadDatabase(view, true)
							})
						}(native.ID)
						return
					}
				}
				a.ui.SetFocus(view.table)
				a.loadDatabase(view, true)
			})
		})
	}()
}
