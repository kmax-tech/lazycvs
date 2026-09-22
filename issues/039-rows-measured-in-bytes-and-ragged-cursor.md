# 039 — Columns measured in bytes, selection bar half-painted

## Problem

Three rendering defects, all visible in a single screenshot of the demo
working copy:

1. **The size column stepped left on non-ASCII names.** `Änderungen.txt`
   pushed its row's size one cell out of alignment, `Straßenbahn.md`
   likewise. With `expected_encoding = latin1` — the default, because
   classic CVS repos predate UTF-8 — German file names are the norm,
   not the exception.
2. **The tree's selection bar ended wherever the text did**, while the
   file list's spanned the pane. The two panes highlighted differently,
   and the tree's bar looked frayed.
3. **Reversing a coloured badge painted it as a solid block.** On the
   selected row, `1M` rendered as a blue rectangle rather than as text —
   the terminal reverses foreground and background per span, so a
   coloured span inside a reversed line inverts twice.

Separately, `colorUntracked` was ANSI 8 — the exact colour of the panel
borders, the muted labels and the separators. A listing of fifteen `?`
rows and one `M` showed sixteen rows of identical grey.

## Cause

1. `truncate()` sliced on `len(s)` (bytes) and the row was assembled
   with `fmt.Sprintf("%-*s")`, whose padding verb also counts bytes. One
   umlaut is two bytes and one display cell, so each one ate a cell of
   padding. The same pair appeared in the History revision list
   (`%-8s` + `truncate` on author names and tags).
2. `tree.go` applied `Reverse(true)` to the composed line without
   padding it to the pane width; `filelist.go` happened to pad its name
   column, which masked the difference.
3. Both panes reversed the *styled* line rather than its plain text.

## Fix

Two shared helpers in `tui/app_layout.go`, used by every pane:

- `padCells(s, w)` truncates to `w` display cells (`ansi.Truncate`) and
  pads with `lipgloss.Width`, replacing both the byte-based `truncate()`
  and the `%-*s` verbs in the file list and the History revision rows.
- `cursorBar(line, width)` strips the line to plain text, pads it to the
  pane width and reverses once — one bar, no blocks, same in both panes.

`colorUntracked` becomes 244: quieter than normal text, brighter than
ignored (239), and no longer identical to the chrome. The hierarchy is
now ignored < untracked < normal.

Tests (`tui/render_test.go`): `padCells` holds its width across
umlauts, ß, exact fits and truncation; two rows with different
encodings stay aligned; the cursor bar spans the pane, keeps its text
and carries a single escape pair; untracked differs from border, muted
and ignored.

## Files touched

- `tui/app_layout.go` (helpers), `tui/filelist.go`, `tui/tree.go`,
  `tui/history.go`, `tui/styles.go`, new `tui/render_test.go`
