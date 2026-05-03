# 003 — Stale diff content shown while scrolling through history revisions

## Problem

When scrolling through revisions in the History tab, the right panel showed the previous revision's diff content while the panel title already reflected the newly selected revision. This caused a jarring mismatch — e.g., title says "Diff — 1.15 ↔ 1.16" but content still shows the diff for 1.17 ↔ 1.18.

Additionally, compare mode required two explicit Space presses (mark reference, then mark target). Moving the cursor after the first Space didn't dynamically update the comparison.

## Cause

`loadHistoryContent()` synchronously updates `diffFromRev`/`diffToRev` (used by the panel title) and dispatches an async CVS diff command, but never set `m.history.ready = false`. Since `ready` stayed `true`, `ViewRight()` kept returning the old viewport content until the new async result arrived.

Mouse click and scroll handlers on the history left panel changed the cursor without triggering any content reload at all.

Compare mode was designed as a two-step selection (Space to mark, Space again to confirm) instead of a reference-based workflow.

## Fix

- Set `m.history.ready = false` whenever `loadHistoryContent()` returns a non-nil command (keyboard navigation, Escape from compare mode, mouse).
- Changed `clickLeft` and `scrollLeft` to return `tea.Cmd`, triggering content reload when the history cursor changes via mouse.
- Auto-compare: when a reference revision is marked (via Space) and the cursor moves away from it, the mode automatically switches to HistoryCompare. Moving back to the reference falls back to HistoryDiff. Space on a different revision moves the reference mark instead of confirming the target.
