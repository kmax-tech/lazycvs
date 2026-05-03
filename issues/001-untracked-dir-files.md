# Files in untracked directories not committable

## Problem

When a directory is new to CVS (no `CVS/` subdirectory), `cvs update -n -q`
only reports the directory itself as `?` — individual files inside are never
listed. As a result, `statusMap` has no entry for those files (empty string),
and the commit dialog treated them as "unchanged" / not committable.

Symptoms:
- Commit dialog showed "Commit (0 of 1)" and "(unchanged)" for files in new dirs
- `cvs add <file>` failed because the parent directory wasn't added first
- Tree showed "Loading..." forever when opening a dir with no subdirectories
  (unrelated root cause: empty flat list when `showFiles=false`)

## Fix

**Status resolution** (`resolveFileStatus` in `app_helpers.go`):
If `statusMap[path]` is empty, check whether `filepath.Dir(path)` has a `CVS/`
subdirectory. If not, the file is untracked — return `"?"`. Used in:
- commit dialog status lookup
- file list display
- staged tab (`Refresh` now takes a resolver function instead of raw map)

**Parent directory auto-add** (`ensureParentDirs` in `dialog.go`):
Before `cvs add <file>`, walk up from the file collecting ancestor dirs that
lack `CVS/` metadata, then `cvs add` them top-down. Applied in `doCommit`,
`addFile`, and staged bulk-add.

**Tree root node** (`tree.go`):
Wrap scanned children in a root `TreeNode` (Path=`.`, Expanded=true) so the
tree always has at least one directory node, even when there are no subdirs.

## Test

`scripts/demo.py` now creates an `experiments/` directory (not added to CVS)
with two files inside to reproduce this scenario.
