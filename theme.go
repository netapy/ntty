package main

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func applyTheme() {
	// Accent small labels, keeping text, backgrounds and native selection neutral.
	tview.Styles = tview.Theme{TitleColor: tcell.ColorTeal, SecondaryTextColor: tcell.ColorTeal}
	selection = tcell.StyleDefault.Reverse(true)
	if colors, ok := resolvedTerminalColors(); ok {
		selection = terminalSelectionStyle(colors)
	}
	navigationSelection = selection
	tview.Borders.HorizontalFocus = tview.Borders.Horizontal
	tview.Borders.VerticalFocus = tview.Borders.Vertical
	tview.Borders.TopLeftFocus = tview.Borders.TopLeft
	tview.Borders.TopRightFocus = tview.Borders.TopRight
	tview.Borders.BottomLeftFocus = tview.Borders.BottomLeft
	tview.Borders.BottomRightFocus = tview.Borders.BottomRight
}
