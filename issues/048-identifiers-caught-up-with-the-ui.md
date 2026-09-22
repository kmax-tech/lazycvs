# 048 — The identifiers still spoke the old vocabulary

## Problem

Three renames in this branch changed what the UI says without changing
what the code calls things, so the package carried two vocabularies:

1. **Staged.** Issue 044 renamed the tab to *Marked* because CVS has no
   index — but `StagedModel`, `TabStaged`, `stagedFile`,
   `stagedBulkAction`, `staging.go` and about 150 references to
   `m.staged` kept the promise alive in the code.
2. **Details.** The Files tab's second layout was `TreeViewDetails`,
   named for the `v` key that used to toggle it. `v` is gone; the
   layout is the diff (issue 043), and "TreeView" named the pane rather
   than the tab-wide layout the value actually selects.
3. **Preview.** Two different things were called preview: the Files
   tab's diff pane (`previewDiff`, `autoLoadPreview`, `renderPreview`)
   and the dialog that displays a block of text (`DialogPreview`, "no
   diff tool configured"). One word, two meanings, in one package —
   the exact fault this branch has been removing from the UI.

## Fix

One vocabulary, applied everywhere:

| was | is | why |
|---|---|---|
| `StagedModel` | `MarkedTabModel` | the tab's panel |
| `App.staged` | `App.markedTab` | `m.marked` would collide with `FileListModel.marked`, the set of marks — one word, one thing |
| `TabStaged`, `StagedMode`, `stagedFile`, `stagedBulk*` | `TabMarked`, `MarkedMode`, `markedFile`, `markedBulk*` | |
| `handleStagedInput`, `keyContext.stagedInput` | `handleCommitInput`, `commitInput` | it handles the commit message field; the old name said which tab it sat in |
| `staging.go` | `marked.go` | |
| `TreeViewMode`, `TreeViewFiles`, `TreeViewDetails`, `App.treeMode` | `FilesLayout`, `LayoutList`, `LayoutDiff`, `App.filesLayout` | it selects the tab's layout, not the tree's |
| `previewDiff/Path/Ready/VP`, `previewMsg`, `autoLoadPreview`, `loadPreviewDiff`, `renderPreview` | `diffData/Path/Ready/VP`, `diffLoadedMsg`, `autoLoadDiff`, `loadDiffFor`, `renderDiffPane` | it is the diff pane |
| `DialogPreview`, `OpenPreview`, `DialogModel.preview` | `DialogNotice`, `OpenNotice`, `DialogModel.scroll` | the dialog shows a notice; the viewport it scrolls serves the help too |

Comments were rewritten rather than word-swapped, so sentences still
read. The two places that still say "Staged" are the ones explaining
why the name changed.

Also removed: `applyTreeMode`, whose only caller was the `v` key.

Two user-visible strings changed, both because they described the old
design: the help row for tab 1 ("Files — tree+filelist OR tree+diff
preview" → "Files — the tree and its files, or a file and its diff")
and the `f` key's keybar label (`f:list` → `f:view`, since it toggles
between two views and `list` named only one of them). The right-pane
title for an empty selection now says `Diff` instead of `Preview`.

Everything else is byte-identical: the keybar and help goldens changed
only in the three case names that contained "staged".

## Files touched

Every file in `tui/` except `favorites.go`, `search.go`, `setup.go`,
`diff.go`; plus `docs/ARCHITECTURE.md`.
