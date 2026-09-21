package main

import (
	"fmt"
	"strings"

	"github.com/rivo/tview"
	"ntty/internal/store"
)

// recoverCreate returns an explicitly unlocked create using its exact saved
// values. newNote should use these as its form defaults.
func (a *app) recoverCreate(parent, title, body string) (string, string, string) {
	intents, err := a.store.Intents()
	if err != nil {
		return parent, title, body
	}
	for _, intent := range intents {
		if intent.Kind == "create" && !intent.Attempted {
			return intent.Parent, intent.Title, intent.Body
		}
	}
	return parent, title, body
}

func intentDescription(intent store.Intent) string {
	state := "draft"
	if intent.Attempted {
		state = "outcome unknown"
	}
	if intent.Kind == "create" {
		return fmt.Sprintf("Create page · %s · %s", intent.Title, state)
	}
	target := "page " + intent.PageID
	if intent.DiscussionID != "" {
		target = "discussion " + intent.DiscussionID
	}
	return fmt.Sprintf("Comment on %s · %s", target, state)
}

func intentDetails(intent store.Intent) string {
	var text strings.Builder
	fmt.Fprintf(&text, "Kind: %s\nCreated: %s\nAttempted: %t\n", intent.Kind, intent.Created.Local().Format("02 Jan 2006 15:04:05"), intent.Attempted)
	if intent.Kind == "create" {
		fmt.Fprintf(&text, "Parent: %s\nTitle: %s\n", intent.Parent, intent.Title)
	} else {
		fmt.Fprintf(&text, "Page: %s\nDiscussion: %s\n", intent.PageID, intent.DiscussionID)
	}
	fmt.Fprintf(&text, "\nExact body:\n%s", intent.Body)
	return text.String()
}

// pendingWrites is deliberately a resolver, not a reconciler: Notion exposes
// no request key that proves identity, so matching title/body is never enough.
func (a *app) pendingWrites() {
	if a.modal {
		return
	}
	if err := a.drafts.FlushAll(); err != nil {
		a.message("Local write: "+err.Error(), true)
		return
	}
	intents, err := a.store.Intents()
	if err != nil {
		a.message("Pending writes: "+err.Error(), true)
		return
	}
	list := tview.NewList().ShowSecondaryText(false).SetHighlightFullLine(false).SetSelectedStyle(selection)
	details := tview.NewTextView().SetWrap(true).SetWordWrap(true)
	status := tview.NewTextView().SetTextStyle(quiet).SetWrap(true)
	refresh := func() {
		list.Clear()
		for _, intent := range intents {
			list.AddItem(tview.Escape(intentDescription(intent)), "", 0, nil)
		}
		if len(intents) == 0 {
			list.AddItem("No pending writes", "", 0, nil)
			details.SetText("Nothing is waiting for resolution.")
		} else {
			list.SetCurrentItem(min(list.GetCurrentItem(), len(intents)-1))
			details.SetText(intentDetails(intents[list.GetCurrentItem()]))
		}
	}
	list.SetChangedFunc(func(index int, _, _ string, _ rune) {
		if index >= 0 && index < len(intents) {
			details.SetText(intentDetails(intents[index]))
		}
	})
	resolve := func(sent bool) {
		index := list.GetCurrentItem()
		if index < 0 || index >= len(intents) {
			return
		}
		intent := intents[index]
		if sent {
			err = a.store.CompleteIntent(intent.ID)
		} else {
			err = a.store.UnlockIntent(intent.ID)
		}
		if err != nil {
			status.SetText("Resolve: " + err.Error())
			return
		}
		if p := a.commentState.pages[intent.PageID]; p != nil && intent.Kind == "comment" {
			delete(p.pending, intent.DiscussionID)
			if sent {
				delete(p.drafts, intent.DiscussionID)
			}
		}
		intents, err = a.store.Intents()
		if err != nil {
			status.SetText("Reload: " + err.Error())
			return
		}
		if sent {
			status.SetText("Cleared by explicit confirmation that it was sent.")
		} else {
			status.SetText("Unlocked by explicit confirmation that it was not sent.")
		}
		refresh()
	}
	refresh()
	buttons := tview.NewFlex().
		AddItem(a.button("It was sent · clear", func() { resolve(true) }), 0, 1, false).
		AddItem(a.button("It was not sent · unlock", func() { resolve(false) }), 0, 1, false).
		AddItem(a.button("Close", a.closeModal), 0, 1, false)
	body := tview.NewFlex().AddItem(list, 0, 1, true).AddItem(details, 0, 2, false)
	pane := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(body, 0, 1, true).
		AddItem(status, 2, 0, false).
		AddItem(buttons, 1, 0, false)
	pane.SetBorder(true).SetBorderStyle(quiet).SetTitle(" Pending writes · inspect in Notion before resolving ").SetBorderPadding(1, 0, 1, 1)
	a.overlay(pane, 100, 30)
}
