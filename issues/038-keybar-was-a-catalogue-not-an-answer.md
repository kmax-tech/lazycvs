# 038 — The keybar listed every key instead of the ones that apply

## Problem

The keybar's second line printed its tab's whole action list, the same
row whatever the cursor sat on. Three consequences:

1. **It did not fit.** On an 80-column terminal — the default width of
   most terminals — the row ended in `…` somewhere inside `[Action]`.
   The `[View]` group was invisible, and nothing said it existed. The
   two-line keybar had been introduced to fix exactly this and had only
   moved it one line down.
2. **It advertised keys that did nothing here.** `a:add` on a tracked
   file, `i:ignore` on a modified one, `r:revert` on a clean row,
   `x:clear marks` with nothing marked, `u:update` on an empty Staged
   tab. Pressing them was a silent no-op, which reads as a broken app.
3. **It never mentioned the marked set**, the one piece of state that
   changes what every action does.

## Cause

Each tab's row was a hard-coded `keyHelp(keys.Diff, keys.Commit, …)`
call with no access to the cursor row, and overflow was left to
`ansi.Truncate` — a display-level cut that cannot know which keys it is
throwing away.

## Fix

The row is now assembled from the keymap registry (issue 037) against
the current `keyContext`:

- **Only what applies.** Each entry carries a predicate that mirrors its
  handler's own gate. `canCommit` calls `isCommittable`, the same helper
  the commit path calls, rather than restating the status set — so `c`
  correctly stays offered on `?` (the commit path adds first), while `a`
  and `i` disappear on tracked files and `r` on a clean row. A directory
  row drops the file actions and keeps the directory ones.
- **The marked set leads the row.** `3 marked ▸ …` precedes the keys
  whenever a worklist exists, and the bulk-capable keys stay live on a
  clean cursor row because the handlers prefer the marks. The counts are
  sliced the way the handlers slice them (`markedRevertable`,
  `markedUntracked`), so a bulk action is only promised when one would
  really run.
- **Overflow is counted, not cut.** When the keys exceed the width, the
  least deserving go first — view toggles, then CVS commands, then the
  tail of the action group, never the conflict hint — and the row ends
  in `+7 (?)`. No row ends in `…` any more.
- **An empty row explains itself** instead of rendering blank.

The keybar reads statuses off the row the user is looking at (the
`FileEntry`, the `TreeNode`, the staged row) rather than calling
`resolveFileStatus`, which stats the disk: this runs on every frame.

One handler had to catch up with its own advertisement: `a` (add)
required a `FileEntry`, which only the file list produces, so it was a
dead key with the cursor on an untracked file in the *tree* pane — where
the new keybar offers it. It now resolves the status from the path.

## Files touched

- `tui/keymap.go` — `keyContext` gains the cursor row's status, the
  sliced mark counts and `isFile`; predicates mirroring the handler
  gates; `barSegmentKeep` (what goes first when space runs out);
  `liveActions`
- `tui/app_layout.go` — `actionRow`: width budget, drop order, marked
  prefix, empty-state line
- `tui/app_keys.go` — `a` resolves by path
- `tui/staging.go` — `SelectedFile` so the context can read a staged
  row's status without the status map
- `tui/dialog.go` — the help banner states the three rules the keys
  actually follow; the old one promised "UPPERCASE extends to the dir /
  every marked file", true only of `A` and `D`
- tests: `TestLiveActionsFollowTheCursorRow` (nine cursor situations,
  asserting both what is offered and what must not be),
  `TestActionRowDropsKeysInsteadOfCutting` (five widths: nothing cut
  mid-word, shown + dropped == applicable),
  `TestActionRowAnnouncesTheMarkedSet`, `TestAddWorksFromTreePane`
