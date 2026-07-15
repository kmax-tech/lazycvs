# 025 — `r` reverted only the cursor file, ignoring the marked set

## Problem

Marking several modified files in the Files (or Favorites) tab and
pressing `r` reverted exactly one file — the one under the cursor.
The rest of the marked set stayed modified with no hint that it had
been skipped.

## Cause

The `r` handler in the Tree/Fav tabs opened the single-file revert
dialog for `selectedPath` unconditionally. The bulk path
(`OpenRevertBulk` → `revertBulkMsg` → `revertPathsCmd`) existed but
was only reachable from the Staged tab. `D` (remove) already used the
"marked set first, cursor fallback" pattern; `r` never got it.

A secondary inconsistency: the bulk dispatchers read per-path
statuses from the raw `statusMap`, while the listing/gates use
`resolveFileStatus` (with its CVS/Entries promotion). A file whose
status only the resolver knows would have gotten the wrong revert
sequence (the C-vs-M distinction decides `rm + update` vs
`update -C`).

## Fix

- `r` in Tree/Fav now mirrors `D`: with marked files it opens ONE
  bulk confirmation over every revertable (M/C) marked path; a marked
  set with nothing revertable gets a banner naming the required
  statuses; only without marks does it fall back to the single-file
  dialog on the cursor row.
- All bulk-revert status snapshots go through `resolveFileStatus`
  instead of raw `statusMap` reads.

Regression test: marked M+C files + `r` → bulk dialog with both
paths and their statuses.

## Files touched

- `tui/app_keys.go`, `tui/app_actions.go`, `tui/app_marks_test.go`,
  `docs/USER_GUIDE.md`
