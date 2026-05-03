# 009 — Right panel title ("Diff — X ↔ Y") disappears after first revision loads

## Problem

When opening a file in the History tab, the right panel title (e.g., "Diff — 1.20 ↔ 1.21") is briefly visible but disappears once the first diff loads. On subsequent cursor moves the title shows stale revision labels from the previous position until the new async result arrives.

## Cause

`diffFromRev` and `diffToRev` (the revision labels that build the right panel border title) were only set in the async result handler (`historyDiffMsg`/`historyContentMsg` in `history.go`), never at dispatch time. Two consequences:

1. **Stale labels on file load**: `historyLoadedMsg` reset most model fields but did NOT clear `diffFromRev`/`diffToRev`. After switching files, the old file's revision labels persisted in the title until the new file's first diff arrived.

2. **Stale labels on cursor move**: After removing `ready = false` from cursor moves (issue 008), the old viewport content stays visible while the new diff loads. But the title ALSO stayed on the old revision's labels, since it was only updated in the result handler. The title didn't update until the async result arrived — a brief but visible mismatch between cursor position and panel title.

## Fix

1. **Clear labels on file load**: Added `m.diffFromRev = ""` and `m.diffToRev = ""` to the `historyLoadedMsg` handler in `history.go`. Prevents stale labels from a previously viewed file.

2. **Set labels eagerly at dispatch time**: In `loadHistoryContent()` (`app_helpers.go`), moved `diffFromRev`/`diffToRev` assignment to BEFORE the cache check. Now the title updates immediately when the cursor moves — no waiting for the async result. The result handler also sets them (redundantly for matching gen, silently for stale gen), so labels are always consistent.

## Interaction with previous fixes

This is the sixth issue in the history scroll rendering chain. Each fix addressed a real symptom but shifted the visual artifact to a different timing window:

| Issue | Symptom | Fix | New artifact |
|-------|---------|-----|--------------|
| 003 | Stale diff content shown (old diff, new title) | Set `ready=false` on dispatch → show placeholder | Placeholder flash on every cursor move |
| 005 | Stale diffs from out-of-order async results | `loadGen` counter to drop stale results | Intermediate "Loading..." flashes during fast scroll |
| 006 | `d` key reloads entire history, duplicate CVS commands | Guard `d` handler with `activeTab != TabHistory` | — |
| 007 | Ghost highlighted rows from ANSI state corruption | Strip inner ANSI before applying Reverse | — |
| 008 | Placeholder flash on every move, mutex serialization | Remove `ready=false`, switch to RunReadOnly, pre-fetch | Title shows stale revision labels |
| **009** | **Title shows stale/wrong revision labels** | **Set labels eagerly at dispatch time** | — |
