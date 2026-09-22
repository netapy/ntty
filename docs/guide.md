# ntty · User guide

A quiet, mouse-friendly Notion notebook for your terminal: *ntty* is Notion in
a `tty`, the Unix word for a terminal. Uses your existing **`ntn`
authentication**, with a sidebar, pinned pages, one rich editable document,
and recoverable local drafts.

## Run

Install a release:

```sh
curl -fsSL https://raw.githubusercontent.com/netapy/ntty/main/install.sh | sh
```

Or build from source (Go 1.27+):

```sh
make build
./bin/ntty
```

Both need a recent [Notion CLI](https://ntn.dev). Tested with `ntn 0.23.4` on
macOS. Authenticate with `ntn login` (or set `NOTION_API_TOKEN`).

`ntty upgrade` replaces the running binary with the latest release after
verifying its checksum; `ntty upgrade --check` only reports.

Cached pages open immediately while Notion refreshes in the background. A small
status spinner indicates loading or syncing. Set `NTTY_REDUCED_MOTION=1` to use
static status icons instead.

On macOS, double-click **Launch.command**. `make install` installs the command
into `~/.local/bin`. A wide terminal (110+ columns) is comfortable.

```sh
./bin/ntty --demo                 # local playground; no Notion requests
./bin/ntty --check                # read-only connection check
./bin/ntty --parent page:<uuid>   # Ctrl+N creates notes in this notebook
./bin/ntty --profile work         # separate local data for another workspace
```

`--profile` names the **local cache only**; authentication remains entirely
with `ntn`. Use separate profiles when switching Notion workspaces/accounts.
`--data-dir` overrides storage. Demo storage is separate from live data.

## Everyday use

Click a page in the sidebar and start typing. There is one editable view.
**Ctrl+K** or the sidebar's **Search**
opens one compact palette for finding pages and running actions. Type `>` to
show only commands. **New note** creates a note; **New child page** creates one
in the open page or data source. A blank parent creates a workspace-level page
where your Notion permissions allow it.

The interface inherits your terminal's foreground and background, including
light themes and transparency. In Ghostty, focus and selection use its resolved
`selection-foreground` and `selection-background`; other terminals use their
standard reverse-video selection. Selection never adds an underline. Hierarchy
uses bold and dim text, and Markdown has no separate hard-coded palette. The
sidebar uses compact one-line rows and separates local Favorites (pins), pages
you actually opened under Recent, and accessible workspace pages. Parent pages
expand into a cached child-page tree with disclosure arrows or Left/Right.
Section collapse state, tree expansion, sidebar width, and full-width document
mode persist locally. Drag the divider to resize, double-click it to reset, and
right-click a sidebar page, breadcrumb, page title, or database row for its
context menu. The title's **···** opens the same page menu: rename, favorite,
child page, copy link, open in Notion, or move to Trash. **Ctrl+K → Trash** restores
known deleted pages. Background metadata checks detect trashed sidebar pages
and keep unsynced local drafts intact. Click breadcrumbs to navigate directly.
Cached search results remain searchable but are not roots.

Headings, bold, italic, underline, strike, inline code, links, lists, tasks,
and quotes render in place while their Markdown remains intact for Notion.
Type `/` on an empty line for a compact block menu: text, heading 1–4, child page,
bullet or numbered list, to-do, quote, code block, inline table, divider, and
links to cached pages. Inline Notion tables render as cell rows while retaining
their exact enhanced Markdown. The slash query is
not written to the draft; Escape keeps a literal `/`. Formatting is also in
the Ctrl+K command palette, which is useful in terminals without Option-as-Meta.
Markdown starts such as `- `, `1. `, `# `, and `> ` transform on Space.
`[]` / `[ ]` become unchecked tasks and `[x]` becomes a checked task as soon
as `]` is typed. Undo restores the literal marker.

Type `@` for a mention picker. It searches people already visible to the
connection and inserts real Notion user mentions; page results combine the local
index with cancellable Notion search after a 300ms typing pause. It also supports `@today`,
`@tomorrow`, `@yesterday`, `@next week`, and exact `YYYY-MM-DD` dates. Personal
access tokens cannot enumerate the whole workspace user directory, so ntty
learns people safely from the authenticated owner, comments, people properties,
and existing page mentions. Escape preserves the query as literal `@text`.

Date mentions and database/page references display readable labels while their
original Notion tags remain intact. Click links to open them; drag to select
their text. Database references open their available data sources in ntty. A
data source opens a read-only table, board/Kanban, list, or gallery view with
local filtering, grouping, sorting, pagination, and mouse/keyboard row opening.
The chosen view, group, and sort are remembered per data source. Use `/` to
focus the filter, `v` or `1`–`4` to change view, `g` to group, `s` to sort, `m`
to load more, and `r` to refresh. Databases whose sources are unavailable
through the API open in Notion.
Read-only meeting-note cards and tab groups stay byte-preserved but render as
compact, clickable objects instead of exposing their enhanced-Markdown tags.

The palette filters cached pages and actions immediately, without API calls.
Choose **Search Notion for “…”** to search beyond the cache. Results reuse the
same one-minute cache as the sidebar. Up/down and Enter navigate; Esc, Ctrl+K,
or clicking outside dismisses it and returns focus to where you were writing.
Menus dim the document behind them, and the mouse wheel scrolls their choices
without taking focus from the search field.

Mouse support comes from tview's native widgets: click-to-position the cursor,
drag/Shift-click selection, double-click word selection, and independent wheel
scrolling in the sidebar, document, and comparison panes. Bracketed paste,
undo/redo, and the macOS clipboard are supported. Click the title to rename it;
Enter or leaving the field saves, while Escape cancels.

The command palette also contains Back/Forward, Reveal in sidebar, document
Outline, Block actions, page sync status, full-width mode, Comments, local
revisions, refresh, and Save a copy. Block actions transform ordinary text,
move a block, duplicate it, or delete it; opaque enhanced-Markdown objects stay
atomic and reject destructive actions.

| Key | Action |
| --- | --- |
| `Ctrl+K` / `Ctrl+P` | Page and command palette (type `>` for actions only) |
| `Cmd+[` / `Cmd+]` | Back / forward when the terminal sends Meta |
| `Cmd+\` | Show / hide the sidebar when the terminal sends Meta |
| `Ctrl+O` | Open a Notion page URL or UUID |
| `Ctrl+N` | New note in the default parent (or workspace) |
| `Ctrl+E` | Move to the end of the current line |
| `Ctrl+S` | Force an immediate sync (normally automatic) |
| `Ctrl+R` | Open local revision snapshots |
| `Ctrl+Z` / `Ctrl+Y` | Undo / redo |
| `Ctrl/Alt+←/→` | Move by word; add Shift to extend selection |
| `Cmd+←/→` / `Cmd+↑/↓` | Line / document edges when the terminal sends Meta |
| `Tab` / `Shift+Tab` | Indent / outdent the current or selected blocks |
| `Ctrl+C` / `Ctrl+X` / `Ctrl+V` | Copy / cut / paste in the editor |
| `Ctrl+L` | Select all in the editor |
| `Ctrl+B` / `Alt+U` | Bold / underline selected text |
| `Cmd+Backspace` / `Ctrl+U` | Delete to the start of the line; keep the list marker |
| `Alt+K` | Delete to end of line (Ctrl+K is reserved for the palette) |
| `@` | Mention a person or insert today/tomorrow/an exact date |
| `Esc` / sidebar `Shift+Tab` | Focus sidebar / return to document |
| `p` / `n` | Pin selected page / new child (sidebar) |
| `g` / `m` | Recent pages / load more (sidebar) |
| `←` / `→` | Collapse / expand page trees in the sidebar |
| `Ctrl+Q` | Quit, keeping unsynced drafts locally |

Terminals decide which Command-key combinations reach terminal applications.
ntty handles Meta/Command navigation, selection, clipboard, and undo when they
are emitted, but does not change terminal key bindings or intercept shortcuts
reserved by macOS.

## Calls and drafts

- All Notion calls are `ntn api` subprocesses with JSON passed through stdin.
  The application never reads your token or keychain itself.
- Requests are serialized, with at least **650ms between starts**. Clean-page
  checks start at **5 seconds**, then back off to **15, 30, and 60 seconds** while
  unchanged. After two minutes without keyboard/mouse activity, checks slow to
  three minutes; returning to the app requests fresh validation. Dirty pages
  are never replaced and instead merge during their normal save preflight.
  Scrolling never fetches pages. On first launch, ntty walks the
  paginated search results once to discover accessible workspace roots and
  child relationships; that index is cached for 24 hours and can be rebuilt
  from **Refresh workspace index** in the command palette. Rapid page switches
  cancel the older read so a slow response cannot replace the newer page.
- Background metadata checks share a budget of one request per **15 seconds**;
  the active page is eligible once a minute, other sidebar pages once every ten
  minutes. Saves and interactive operations take precedence over new metadata
  checks. Failed background reads share a cooldown of 15 seconds to five minutes;
  explicit refresh remains available. Cached breadcrumb paths are reused until
  their metadata changes.
- Reopening a page paints its local cache immediately, then validates it in the
  background instead of trusting a stale time window. Search results have a
  **1-minute in-memory cache**; API search pages contain up to 100 results.
- Every edit is queued to one serial, coalescing atomic draft writer so typing
  never waits for disk sync. Quit and remote sync flush the latest draft first.
  Workspace metadata and comment drafts use the same background writer; unchanged
  page metadata causes no disk write or cache invalidation.
  Autosave waits for exactly **3 seconds idle**. A transient failure starts a
  fresh 3-second retry window; there is no second hidden throttle. Each save
  normally costs one read and one write.
- HTTP 429 errors back off exponentially, respecting `Retry-After` when `ntn`
  includes it in the error. Raw PATCH requests are never blindly repeated after
  an ambiguous server error: ntty first reconciles the exact persisted
  preflight/target pair. Create/comment requests are recorded before sending.
  Uncertain attempts remain locked across restarts: use **Pending writes** in
  the command palette to inspect the exact request and resolve its outcome after
  checking Notion. Matching titles or comment text alone never proves success.
- Before saving, compare the remote Markdown with the draft's base. Independent
  local and Notion edits merge automatically across blocks and within the same
  paragraph; incompatible insertions at the same boundary pause sync and keep
  the draft. A paused draft stays paused
  while you type; `Ctrl+S` explicitly retries the full preflight, and restoring
  a revision starts a fresh safe autosync. Saves use unique server-side exact-match
  `update_content` targets. Pure block insertion at the document's start or end
  can use positional `insert_content` when a protected object leaves no editable
  anchor; the existing page remains byte-preserved, and uncertain responses use
  the same persisted retry checkpoint. Existing empty pages remain local-only because the
  API has no safe atomic revision precondition, so this is not a collaborative
  editor/CRDT. New blank pages are seeded with an editable empty-block anchor.
- New blank paragraphs are serialized as Notion `<empty-block/>` objects, so
  several blank blocks cannot truncate the rest of a write. Expiring signatures
  on Notion-hosted file URLs are rebased without creating false conflicts, even
  when the base, recovered draft, and current response contain three different
  signatures; the underlying file identity must still match exactly.
- Literal dollar signs in prose are escaped by the editor. Notion's alternate
  literal-dollar spelling and collapsed skipped list indentation are recognized
  when confirming saves; code contents remain exact. Page mentions are compared
  by Notion page identity rather than display label or URL spelling. Uncertain
  writes reconcile before further writes; incompatible additions at the same
  insertion point remain a conflict rather than being concatenated on each retry.
- Protected Notion objects and metadata are checked during editing. An operation
  that would remove or rewrite an existing one is rejected immediately instead
  of creating a draft that can only fail later during sync. New valid structured
  blocks such as `/table` are allowed and remain atomic.
- New typing during an in-flight save is rebased onto the exact content Notion
  confirms. If that newer typing overlaps a remote change, sync fails closed and
  keeps both versions instead of treating remote text as a local deletion.
  Transient failures retry automatically with the normal backoff. Conflicts and
  structurally unsafe updates remain local and can be reviewed through
  **Compare with Notion** in the command palette. Recovered drafts safely resume
  autosync after launch using the same remote preflight checks.
- If a PATCH response is lost after a three-way merge, ntty persists the exact
  preflight base, merged attempt, and originating draft. The next autosave—or a
  restart—therefore reconciles safely whether Notion received the write or not,
  before applying any newer typing.
- Edits to incomplete/truncated Markdown stay local until a complete base
  version is available. Notion's child-page/database deletion protection stays enabled.
- Quitting waits for an active write to finish; other unsynced edits remain
  local. One running instance per profile prevents conflicting draft writes.

Data is under `~/Library/Application Support/ntty/<profile>/` on macOS,
or the platform's config directory elsewhere. Drafts from the old `ntn-tui`
folder are moved automatically on first launch. `pages/*.json` contains the
text, base version, dirty flag, and any ambiguous-write checkpoint; files are
private (0600). Keep this folder
to preserve drafts. `state.json` holds pins, the recent/page index, and local
interface state.

## Scope

This is a **rich Markdown notebook**, not a replica of Notion's block canvas.
Database/data-source results have read-only table, board/Kanban, list, and
gallery views and can be opened as pages. Property editing, native saved-view
configuration, image rendering, and drag-and-drop card/block rearrangement are
not implemented.
Advanced Notion content may appear as enhanced Markdown/XML tags. Search is by
title, as provided by Notion's search API. A saved page may be normalized by
Notion's Markdown round-trip.

Notion's public API does not expose its own Favorites, browser Recents, or the
sidebar's teamspace/private grouping. ntty therefore keeps Favorites and
actually opened Recents locally, while its Workspace section indexes every
page relationship the API search can access and presents accessible roots as a
collapsible tree.

Comments use Notion's public page-comment API. Open discussions and replies are
supported; resolved discussions and native server-side page history are not
available from that API. `Ctrl+R` instead provides up to 50 immutable local snapshots
per page around fetch, sync, refresh, restore, and conflict-resolution actions.

## Development

```sh
make test     # race detector, safe-write/pacing/draft tests, simulated mouse UI
make check    # go vet
make demo
```

Go + [tview](https://github.com/rivo/tview) for the native terminal widgets.
No server, JavaScript runtime, or separate Notion SDK.
