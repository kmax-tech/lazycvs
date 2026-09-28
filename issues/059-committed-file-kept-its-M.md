# 059 — A committed file went on showing M

## Problem

Commit a file from the Favorites tab with no favorites configured, and
the file keeps its `M` in the file list. Forever: `s`, another commit,
switching directories — nothing clears it. CVS itself is right,
`cvs status` on the file reports `Up-to-date` at the new revision. Only
lazycvs still says the file is modified.

The commit path does the right thing. `commitDoneMsg` drops the marks
and fires `refreshStatusForPaths`, the dir scan comes back clean,
`applyStatuses` deletes the stale entry from `statusMap` and refreshes
the tree and the Marked tab. Then it calls `updateFileList`, and that is
where the refresh died:

```go
case TabFavorites:
    dir = m.favorites.SelectedPath()
default:
    return
}
if dir == "" {
    return
}
```

`SelectedPath()` returns `""` when nothing is favorited. So the rebuild
was skipped — and the file list is the one place that does not read
`statusMap` at render time. `updateFileListForDir` bakes the status code
into each `cvs.FileEntry` when it builds the rows. Rows nobody rebuilds
keep the code they were born with.

The `default: return` hid the same hole for the Marked and History
tabs. The comment above the function claimed those tabs "don't have a
file list", but the left-pane tabs only decide what drives the right
pane — the file list is on screen on all four.

## Fix

**The rebuild no longer depends on the tab having a directory.** When
the active tab names none, `updateFileList` falls back to the directory
the list is already showing (`m.filelist.dir`). Empty only before the
first build, and then there is nothing to refresh.

**`markedStatusMsg` rebuilds too.** It called `applyStatuses` and
stopped, so the per-file `cvs status` behind the Marked tab updated
every consumer except the rows the user was looking at.

**A same-dir rebuild keeps the user's place.** The fallback makes
rebuilds far more frequent, and `SetFiles` reset the cursor to the top
and refolded every subdir on every call. That is right when the user
navigates to a different directory and wrong when the same directory is
rebuilt under them by a status refresh. `SetFiles` now distinguishes the
two: on a same-dir rebuild it restores the cursor by row identity (file
path, or subdir path for a directory header) and carries the fold state
across.

## Tests

`TestUpdateFileListRefreshesWithoutTabDirectory` walks the three tabs
that name no directory and asserts a cleared `statusMap` reaches the
listing. `TestUpdateFileListNoopBeforeFirstBuild` pins the empty case.
`TestSetFilesKeepsPlaceOnSameDirRebuild` holds the cursor and the folds
for a same-dir rebuild, and confirms a directory change still starts at
the top. All four fail against the old code with the reported symptom:
`after refresh top.txt status = "M", want empty`.
