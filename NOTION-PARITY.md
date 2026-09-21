# Notion menu reference

Observed in the authenticated Notion web app through the existing Helium browser
on 2026-09-21, using the disposable ntty sync-test page. This is an observed
inventory, not a claim that every command is implemented in ntty.

## `/` — empty query

- Suggested: AI Meeting Notes; HTML (Embeds, New); AI Image (New); To-do list.
- Basic blocks: Text; Heading 1–4; Bulleted list; Numbered list; To-do list;
  Toggle list; Page; Callout; Quote; Table; Divider; Link to page.
- Media: Image; Video; Audio; Code; File; Web bookmark.

The initial menu exposes shortcuts beside commands: `#`, `##`, `###`, `####`,
`-`, `1.`, `[]`, `>`, `"`, `---`, and triple backticks. It is anchored beside
the insertion point. Typing filters results; arrows navigate, Enter selects,
and Escape closes the menu while retaining the typed text.

## `/database` — filtered results

Database - Inline; Database - Full page; Table view; Board view; Gallery view;
List view; Feed view; Dashboard view; Calendar view; Timeline view; Map view;
Vertical bar chart; Horizontal bar chart; Line chart; Donut chart; Number chart;
Form; Linked view of data source; Asana; GitLab; GitHub.

Plain Table and database Table view are distinct commands and data models.
This filtered list includes commands absent from the initial empty-query list.

## `@` — empty query

- Date: Today; Remind me — Tomorrow 9am.
- People: suggested workspace people, including the current user; Invite….
- Link to page: recent/relevant pages; more results.
- Page creation: Add new sub-page; Add new page in….

For `@tomorrow`, the date becomes Tomorrow, a corresponding reminder is offered,
people invitation uses the query, page search returns matching titles, and the
creation actions propose the query as the new page title.

## Current implementation and remaining gaps

ntty supports basic text/list/headings (including Heading 4), child-page creation,
quotes, inline tables, dividers, code, and page links. Its mention picker combines
dates, genuine user mentions, cached page mentions and debounced remote page search.
Both menus preserve literal input on Escape.

Still to implement and verify: toggle/callout insertion and rich rendering;
menu section headers and shortcut hints; media insertion; reminder semantics;
mention-driven page creation; database/view creation; and Notion-native AI actions.
Read-only database browsing already exists independently of block insertion.
Unsupported actions must gain real backend behavior before being presented as
working commands. No private browser endpoints or extracted credentials are used.
