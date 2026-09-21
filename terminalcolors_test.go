package main

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestParseTerminalColors(t *testing.T) {
	for _, test := range []struct {
		name, config                                                     string
		foreground, background, selectionForeground, selectionBackground int32
	}{
		{"light", "theme = light:Paper,dark:Ink\nbackground = #fffcf0\nforeground = navy\nselection-background = #cecdc3\nselection-foreground = black\n", 0x000080, 0xfffcf0, 0x000000, 0xcecdc3},
		{"dark inherited", "theme = light:Paper,dark:Ink\nbackground = 282828\nforeground = #ebdbb2\nselection-background = cell-foreground\nselection-foreground = cell-background\n", 0xebdbb2, 0x282828, 0x282828, 0xebdbb2},
	} {
		t.Run(test.name, func(t *testing.T) {
			colors := parseTerminalColors(test.config)
			if !colors.hasForeground || !colors.hasBackground || !colors.hasSelectionForeground || !colors.hasSelectionBackground {
				t.Fatalf("missing parsed colors: %+v", colors)
			}
			if colors.foreground.Hex() != test.foreground || colors.background.Hex() != test.background || colors.selectionForeground.Hex() != test.selectionForeground || colors.selectionBackground.Hex() != test.selectionBackground {
				t.Fatalf("colors: fg=%v bg=%v selection=%v/%v", colors.foreground, colors.background, colors.selectionForeground, colors.selectionBackground)
			}
		})
	}
}

func TestTerminalSelectionStylePreservesTerminalBackground(t *testing.T) {
	colors := parseTerminalColors("background = #fffcf0\nselection-background = #205ea6\nselection-foreground = #fffcf0\n")
	style := terminalSelectionStyle(colors)
	fg, bg, attrs := style.Decompose()
	if fg.Hex() != 0xfffcf0 || bg.Hex() != 0x205ea6 || attrs != 0 {
		t.Fatalf("selection style: fg=%v bg=%v attrs=%v", fg, bg, attrs)
	}
	baseFG, baseBG, _ := tcell.StyleDefault.Decompose()
	if baseFG != tcell.ColorDefault || baseBG != tcell.ColorDefault {
		t.Fatalf("base style no longer preserves terminal colors: fg=%v bg=%v", baseFG, baseBG)
	}
}

func TestTerminalSelectionStyleUsesDefaultForegroundWhenUnset(t *testing.T) {
	colors := parseTerminalColors("selection-background = red\nselection-foreground = not-a-color\n")
	fg, bg, attrs := terminalSelectionStyle(colors).Decompose()
	if fg != tcell.ColorDefault || bg != tcell.ColorRed || attrs != 0 {
		t.Fatalf("selection style: fg=%v bg=%v attrs=%v", fg, bg, attrs)
	}
}
