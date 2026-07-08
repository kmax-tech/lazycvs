# 022 — Modified files deeper than one level had no selectable row

## Problem

The tree pane showed aggregate badges like `4M` on a directory, but
the right-pane file list offered no `M` row to select when the
modified files lived two or more levels below the listed directory
(e.g. `part-network-protocol/exercises-de/foo.tex` while listing the
working-copy root). In `sub` mode the subdir group showed only its
direct files; in `flat` mode the deep files were missing entirely.
The user could see the counts but not act on the files without
manually expanding the tree all the way down.

## Cause

`updateFileListForDir` walks exactly one level: direct files plus
each immediate subdir's direct files. The follow-up pass that patches
in entries from `statusMap` (originally for server-only `U` files)
was capped at depth 2 — a `switch len(parts) { case 1: …; case 2: … }`
silently dropped anything deeper. Meanwhile the dir badges are
computed from the full-subtree `statusMap`, so badge counts and
listable rows disagreed. `flat` mode additionally showed only
`m.files` (direct children) by design, so even depth-2 changes never
appeared there.

## Fix

- The statusMap pass now surfaces changed files at **any** depth,
  attaching them to their top-level subdir group (synthesizing the
  group if the subdir only exists server-side). On-disk deep files
  get their real size and are not flagged `(server)`.
- `flat` mode also lists changed files from below the directory,
  rendered as dir-relative paths; clean deep files stay hidden
  (structural browsing is what `sub`/`tree` modes are for).
- All listing buckets are sorted by path afterwards — the appended
  statusMap entries come from random map iteration order and would
  otherwise jump around between refreshes.

Invariant after the fix: every count in a directory badge has a
selectable row in every view mode.

## Files touched

- `tui/app_files.go` — depth-unlimited statusMap surfacing + sorting
- `tui/filelist.go` — flat mode includes deep changed files
- `tui/app_files_test.go` — regression tests (deep M file gets a row
  in the group; flat view shows it; clean deep files stay hidden)
