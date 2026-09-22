# 040 — The file pane reported two different totals

## Problem

The file pane's header and footer disagreed about how many files it was
showing. A listing of fourteen files said `— 14 files` at the top and
`10 of 15` at the bottom. Neither number was wrong on its own terms;
they were counting different things, and nothing on screen said so.

The header also carried its state as shorthand: `./ (flat) -ign`.
`-ign` names neither the setting nor which way it points — ignored files
hidden, or ignored patterns off? The one state the user most often
wonders about ("why don't I see that file?") was the least legible thing
on the panel.

## Cause

Both surfaces counted for themselves. The header walked `rows()` and
counted entries with a file; the footer took `len(rows())`, which
includes the directory header row and any subdirectory headers. Two
independent counts of the same thing, as in issue 024 — the difference
here was visible on screen rather than buried in a gate.

## Fix

`FileListModel.FileCount()` is the count, and `CursorFileIndex()` is the
cursor's position among the files. Both surfaces read them:

- header: `— 14 files, 2 marked`
- footer: `3 of 14` on a file row, `14 files` while the cursor rests on
  a directory header (which has no position among files)

The header's state becomes named chips:
`./ · flat · ignored:hidden · filter:M — 14 files`. The ignored chip is
always present and always says which way it points, because "where is my
file?" deserves an answer on screen rather than in the help.

Tests: `TestFileListHeaderAndFooterAgree` (the two surfaces report one
total, and the footer switches between position and total),
`TestFileListHeaderNamesItsState` (each chip appears, and the old
shorthand is gone).

## Files touched

- `tui/filelist.go` (`FileCount`, `CursorFileIndex`, header chips)
- `tui/app_layout.go` (`fileListInfo`, used by both tabs that show the
  file pane)
- `tui/render_test.go`
