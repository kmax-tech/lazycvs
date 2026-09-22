# 051 — Local untracked directories were labelled "(server)"

## Problem

`build/`, `experiments/` and `tests/` sit in the working copy, untracked.
The file listing showed each of them with `(server)` in the size column
— the marker for an entry CVS knows about but that does not exist
locally yet. Exactly backwards: they are local and the server has never
heard of them.

## Cause

`updateFileListForDir` surfaces status-map entries the disk walk did not
produce. For a direct child it concluded:

```go
// Direct child of dir. ReadDir listed everything on disk,
// so an unseen direct child can only be server-side.
```

The walk lists *files*; directories are collected separately as
subdirectory groups, and `seen` was built from the files only. So every
untracked top-level directory — `cvs -n update` reports one as a single
`? build` line — fell through as "unseen" and was declared server-side
by assumption. The deeper branch, a few lines below, asked the disk
instead and got it right.

## Fix

Ask the disk at every depth: the stat that the deep branch already did
now runs before the direct-child case, so the entry carries `IsDir`,
its size and its encoding flag whichever depth it came from. A
directory now renders as `build/` with no size, and `(server)` is left
for entries that really are only on the server.

## Files touched

- `tui/app_files.go`
