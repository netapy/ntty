<div align="center">

# ntty

### Your Notion workspace. A little closer to the keyboard.

A quiet, mouse-friendly terminal notebook.<br>
Write, navigate, and stay in sync—without leaving your terminal.

[Getting started](#getting-started) · [The experience](#the-experience) · [Keybindings](#make-yourself-at-home) · [User guide](docs/guide.md)

![Go 1.24+](https://img.shields.io/badge/Go-1.24%2B-8be9fd?style=flat-square&labelColor=282a36)
![V1](https://img.shields.io/badge/status-V1-bd93f9?style=flat-square&labelColor=282a36)
![Built for the terminal](https://img.shields.io/badge/built_for-the_terminal-50fa7b?style=flat-square&labelColor=282a36)

</div>

![ntty showing a fictional Lunar Studio workspace in a Dracula-inspired terminal](docs/images/workspace.svg)

<p align="center"><sub>Actual ntty widgets, fictional workspace. Screen cells rendered as SVG with a Dracula-inspired palette.</sub></p>

## A notebook, not another browser tab.

Some days you just want to open a page, write something, and get back to work.
**ntty** brings Notion into your terminal with one editable document, a compact
page tree, and the small comforts that make a tool feel like home.

Mouse when you want it. Keyboard when you don't. Your terminal's colours either way.

## The experience

| Write naturally | Find your way | Keep the work safe |
| :--- | :--- | :--- |
| Rich text, headings, lists, tasks, and inline tables | Searchable command palette and expandable page tree | Autosave after three seconds of idle time |
| `/` block commands and `@` mentions | Clickable breadcrumbs that follow real parents | Local drafts, undo/redo, and revision snapshots |
| Mouse selection, Unicode, and clipboard | Right-click menus, favourites, rename, Trash and restore | Remote refresh and conservative conflict handling |

**Databases belong here too.** Browse table, board, list, and gallery layouts.
Select actual Notion saved views to use their server-side filters and sorts, or
filter loaded rows with `Property: value`. Open a row to work on its page.

**Less interface when you need it.** Collapse the sidebar, keep routine status
messages tucked away, and let the document take the space.

![ntty command palette in a fictional workspace](docs/images/commands.svg)

## Getting started

You'll need **Go 1.24+**, a recent **[Notion CLI (`ntn`)](https://ntn.dev)**,
and a terminal. macOS is the primary tested platform.

```sh
# Install and authenticate ntn first: https://ntn.dev
ntn login

git clone https://github.com/netapy/ntty.git
cd ntty
make install

# ~/.local/bin must be on your PATH
ntty
```

Want to look around first? No account needed:

```sh
ntty --demo
```

ntty delegates authentication and API requests to `ntn`. It doesn't ask you to
copy credentials into another app or run a local server.

<details>
<summary><strong>A few useful startup options</strong></summary>

```sh
ntty --check                    # Read-only connection check
ntty --profile work             # Separate local cache and drafts
ntty --parent page:<uuid>       # Default parent for new notes
ntty --data-dir /path/to/data   # Choose a storage directory
```

Profiles isolate local data; they do not switch the account authenticated in `ntn`.

</details>

## Make yourself at home

| Action | Shortcut |
| :--- | :--- |
| Find a page or command | `Ctrl+K` · type `>` for commands |
| Create a note | `Ctrl+N` |
| Insert a block / mention | `/` on an empty line · `@` |
| Toggle the sidebar | `Ctrl+\` · click `☰` |
| Save immediately | `Ctrl+S` |
| Undo / redo | `Ctrl+Z` / `Ctrl+Y` |
| Local revisions | `Ctrl+R` |
| Copy / cut / paste | `Ctrl+C` / `Ctrl+X` / `Ctrl+V` on macOS |
| Quit, retaining local drafts | `Ctrl+Q` |

Click to place the cursor. Drag to select. Scroll the pane under your pointer.
Right-click a page, breadcrumb, title, or database row for its menu. In a version
comparison, use `←` / `→` or click a pane, then `Enter` to choose it.

## Sync, with a memory

Edits are saved locally before sync. ntty uses Notion's enhanced Markdown API,
reads before writing, and sends targeted updates. Clean active pages refresh
automatically; edits made while a save is running are rebased onto the result.

Uncertain writes are reconciled before another write. If versions can't be
merged confidently, both are retained for comparison. Protected embeds stay
intact; inline references can be removed without deleting their destinations.

## An honest V1

ntty is an independent client, not an official Notion product. The public API
sets the boundaries: database views are read-only, and calendar/timeline-style
views fall back to tables. Some complex embeds remain atomic or need the web app.
Notion AI and every web-editor interaction are not replicated.

The goal is a useful daily notebook, with clear limits. See the
[full guide](docs/guide.md) and [parity notes](NOTION-PARITY.md).

## Build & contribute

```sh
make build
go test ./...
go test -race -timeout 120s ./...
go vet ./...
```

Go, [tview](https://github.com/rivo/tview), [tcell](https://github.com/gdamore/tcell),
and the official Notion CLI. No JavaScript runtime. No separate sync server.

The README images are reproducible, account-free captures of the real TUI:

```sh
NTTY_SCREENSHOTS=1 go test . -run '^TestReadmeScreenshots$' -count=1
```

Found a rough edge? [Open an issue](https://github.com/netapy/ntty/issues) with
reproduction steps and your terminal. Use fictional content in examples.

---

<p align="center"><sub>ntty = Notion + tty. A little less switching. A little more making.</sub></p>
