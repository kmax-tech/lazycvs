# 043 — `d` answered a different question in a different tab

## Problem

`d` (diff) switched to the History tab and loaded the file's revision
log. But "what did I change here?" and "who changed this, and when?"
are two different questions, and the second one arrives with its own
navigation: a revision list, compare anchors, four modes, and no way
back to the file list except Esc and re-finding the row.

Meanwhile the app already had a diff pane — the Detail layout — reachable
only through `v`, an unexplained toggle that flipped the left pane
between "directories" and "directories and files" and left the cursor
wherever it happened to land. Two ways to see a diff, neither obvious,
and the more useful one hidden behind the more cryptic key.

## Fix

`d` toggles the diff layout in place, with the cursor kept on the file:

- **Opening**: the tree switches to showing files, the cursor lands on
  the same file, focus moves to the tree pane (so `j`/`k` walks from
  file to file with the diff following), and the right pane shows the
  diff.
- **Closing** (`d` again, or `Esc`): the file list comes back with the
  cursor on the same file and focus back on the list.

Both directions go through `revealPath` — the landing procedure the
fuzzy finder and the command-line path argument already share — so a
file ends up in the same place however it was chosen.

`v` is gone. With `d` opening the layout, a separate key for it was a
second door to one room, named after the mechanism rather than the
question.

From the Favorites or Staged tab, `d` lands in the Files tab: the diff
layout lives there, and Favorites' left pane is a list of pinned
directories, not a tree. That is one deliberate jump, to the file the
cursor was on, rather than the old jump into a different information
model.

The History tab keeps its own `d` (diff against the previous revision)
and stays reachable with `4` and `Enter` on a revision.

Tests: `TestDiffKeyTogglesTheDiffPaneInPlace` (opens on the same file
with the cursor and focus placed for browsing, closes back onto the
same row) and `TestEscapeClosesTheDiffPane`.

## Files touched

- `tui/app_history.go` — `toggleDiffView`, `showingDiffFor`
- `tui/app_keys.go` — `d` toggles, `v` handler gone, `Esc` closes
- `tui/tree.go` — `FocusOnPath`
- `tui/keys.go`, `tui/keymap.go` — `keys.ViewMode` gone, `d` help text
- `tui/app_reveal_test.go`, goldens
