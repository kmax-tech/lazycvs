# 046 — "sub" was a fold state dressed as a view mode

## Problem

The file pane cycled through three views with `f` — `flat`, `sub`,
`tree` — plus a `t` key that jumped straight to `tree` because reaching
it could take two presses. Four ways to answer one question ("which
files am I looking at"), and the header said `(sub)` without saying what
that was.

`sub` and `tree` were not two views. They were the same branch of
`rows()`, differing in a single boolean:

```go
expanded := m.viewMode == FileViewSub || sd.Expanded
```

`sub` **is** `tree` with every subdirectory unfolded.

## Cause

The fold state of the tree was never given a key, so a second mode was
invented to carry it. Once it existed as a mode, `t` was added to make
the now-three-state cycle bearable.

## Fix

Two views, because there are two questions:

- `flat` — what changed here
- `tree` — let me walk the directories

`f` toggles between them. `~` in the file pane folds or unfolds every
subdirectory at once — the old `sub`, as a gesture rather than a mode,
and the same "all of it" meaning the key already has in the tree pane.
`t` is gone; a two-state toggle needs no shortcut.

The fold state is sticky for the session (`FileListModel.expandAll`), so
walking into another directory keeps what the user chose — the one
thing the old `sub` mode did well. The header names it:
`./ · tree · all open · ignored:hidden — 21 files`.

`~` is offered in the keybar only where it means something: the file
pane, in the tree view. That needed `keyContext.listMode` — a
deliberate extension of the context rather than a peek at the App.

## Files touched

- `tui/filelist.go` — `FileViewSub` gone, `ToggleViewMode`,
  `ToggleExpandAll`, `expandAll` inherited by `SetFiles`, header chip
- `tui/app_keys.go` — `f` toggles, `~` folds in the file pane, `t` gone
- `tui/keymap.go`, `tui/keys.go` — `keyContext.listMode`,
  `canFoldSubdirs`, the `~` entry, `keys.TreeList` removed
- `tui/render_test.go`, `docs/USER_GUIDE.md`, goldens
