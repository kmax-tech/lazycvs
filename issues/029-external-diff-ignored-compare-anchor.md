# 029 — External diff tool ignored the compare anchor

## Problem

In the History tab, `space` pins a revision as the "from" side of a
comparison and `w` swaps the "to" side to the working copy. The built-in
diff view honoured both. `E` (external diff tool) did not: anchoring 1.85
and putting the cursor on 1.90 opened 1.89 ↔ 1.90 in the tool, not
1.85 ↔ 1.90. There was no way to feed two freely chosen revisions to an
external diff/merge tool.

## Cause

Two independent resolutions of the same question. `currentDiffKey`
(`tui/app_history.go`) resolved cursor + anchor + `vsWorking` into a
(from, to) pair for the right pane, while `launchExternalDiffForCurrent`
(`tui/external.go`) hardcoded `rev.PrevNumber ↔ rev.Number` and never
looked at the anchor. The pane's Diff-mode guard sat at the top of
`currentDiffKey`, so `E` could not reuse it either — `E` works in Content
and Blame mode too, where that guard returns an empty pair.

## Fix

Split the guard from the resolution:

- `comparePair()` — the resolution (cursor, anchor, `vsWorking`,
  working-copy pseudo-row), mode-independent.
- `currentDiffKey()` — the Diff-mode gate around it, unchanged behaviour
  for the pane's cache/serve path.

`launchExternalDiffForCurrent` now calls `comparePair()` and maps each
side through the new `sideForRev`, which turns the `workingRev` sentinel
into the on-disk file and everything else into a `cvs co -p` temp file.
An empty pair (initial revision without anchor, both sides pinned to
working, from == to) makes `E` a no-op instead of launching the tool on a
bogus pair.

Tests in `tui/external_test.go` cover the anchor in all three pane modes,
the no-anchor parent fallback, `vsWorking` + anchor, the empty pair, and
that `currentDiffKey` keeps its Diff-mode gate.
