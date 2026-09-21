package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
)

type terminalColors struct {
	foreground, background                         tcell.Color
	selectionForeground, selectionBackground       tcell.Color
	hasForeground, hasBackground                   bool
	hasSelectionForeground, hasSelectionBackground bool
}

var terminalColorsOnce = sync.OnceValues(discoverTerminalColors)

func resolvedTerminalColors() (terminalColors, bool) { return terminalColorsOnce() }

func discoverTerminalColors() (terminalColors, bool) {
	if !strings.EqualFold(os.Getenv("TERM_PROGRAM"), "ghostty") {
		return terminalColors{}, false
	}
	path := ghosttyExecutable()
	if path == "" {
		return terminalColors{}, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "+show-config").Output()
	if err != nil {
		return terminalColors{}, false
	}
	colors := parseTerminalColors(string(out))
	return colors, colors.hasSelectionBackground
}

func ghosttyExecutable() string {
	if path, err := exec.LookPath("ghostty"); err == nil {
		return path
	}
	if executable, err := os.Executable(); err == nil && strings.EqualFold(filepath.Base(executable), "ghostty") {
		return executable
	}
	const macApp = "/Applications/Ghostty.app/Contents/MacOS/ghostty"
	if info, err := os.Stat(macApp); err == nil && !info.IsDir() {
		return macApp
	}
	return ""
}

func parseTerminalColors(config string) terminalColors {
	var colors terminalColors
	var selectionForeground, selectionBackground string
	for _, line := range strings.Split(config, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "foreground":
			colors.foreground, colors.hasForeground = parseTerminalColor(value)
		case "background":
			colors.background, colors.hasBackground = parseTerminalColor(value)
		case "selection-foreground":
			selectionForeground = value
		case "selection-background":
			selectionBackground = value
		}
	}
	colors.selectionForeground, colors.hasSelectionForeground = parseSelectionColor(selectionForeground, colors)
	colors.selectionBackground, colors.hasSelectionBackground = parseSelectionColor(selectionBackground, colors)
	return colors
}

func parseTerminalColor(value string) (tcell.Color, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) == 6 && !strings.HasPrefix(value, "#") {
		if hex, err := strconv.ParseInt(value, 16, 32); err == nil {
			return tcell.NewHexColor(int32(hex)), true
		}
	}
	color := tcell.GetColor(value)
	return color, color.Valid()
}

func parseSelectionColor(value string, colors terminalColors) (tcell.Color, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "cell-foreground":
		return colors.foreground, colors.hasForeground
	case "cell-background":
		return colors.background, colors.hasBackground
	default:
		return parseTerminalColor(value)
	}
}

func terminalSelectionStyle(colors terminalColors) tcell.Style {
	style := tcell.StyleDefault.Background(colors.selectionBackground)
	if colors.hasSelectionForeground {
		style = style.Foreground(colors.selectionForeground)
	}
	return style
}
