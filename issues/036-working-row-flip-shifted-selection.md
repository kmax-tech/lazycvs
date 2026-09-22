# 036 — Appearing working row silently shifted cursor and anchor

## Problem

When a file went dirty (or clean) while its History was open — e.g. a
pair merge or a `B` backup restore — the pseudo "working" row appeared
(or vanished) at index 0 and every revision row shifted by one. Cursor
and compare anchor kept their numeric indices, so the selection silently
slid onto a neighbouring revision: anchor 1.85 became 1.86 without the
user touching anything.

## Cause

`applyWorkingState` (`tui/history.go`) only clamps indices that fall out
of range; it has no notion of "same revision, new index". All three
callers shared that behavior, though only one of them has a stable list.

## Fix

`RefreshWorkingState` — the only caller where the revisions list is
unchanged and a live selection exists — now detects the
`hasWorkingRow` flip before the clamp runs and shifts cursor and anchor
by one in the corresponding direction. An anchor sitting on the
vanishing working row is cleared (its target no longer exists).
`ApplyRevisions`/`AppendRevisions` deliberately keep the old behavior:
there the list itself changed and row indices carry no identity.

Tests: `TestRefreshWorkingStateShiftsSelection` (round-trip: pair stays
1.85 ↔ 1.90 through dirty→clean) and
`TestRefreshWorkingStateClearsAnchorOnVanishedWorkingRow` in
`tui/external_test.go`.
