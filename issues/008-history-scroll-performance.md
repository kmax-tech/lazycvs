# 008 — History scroll performance: exclusive locks, placeholder flash, no pre-fetch

## Problem

Scrolling fast through history revisions caused visual glitches:
- Diff panel flashed to a "Loading..." placeholder then back to content on every cursor move
- All read-only CVS operations (diff, log, annotate, checkout -p) used the exclusive write mutex, serializing every async load — including stale pre-empted requests that would be dropped by loadGen
- No pre-fetching of adjacent revisions, so every scroll position was a cache miss on first visit

## Cause

1. `exec.Run` (exclusive lock) was used for all CVS calls in the history tab. With fast scrolling, stale diff goroutines queued behind the mutex, delaying the current diff.
2. `m.history.ready = false` was set on every cursor move, causing ViewRight to return a single-line placeholder. This produced a dramatic visual flash (full content → near-empty → full content).
3. No mechanism to pre-populate the cache for adjacent revisions.

## Fix

1. Switched all read-only history operations to `exec.RunReadOnly` (shared RLock): `loadHistory`, `loadWorkingDiff`, `loadRevisionDiff`, `loadRevisionContent` (via `catRevisionStdout`), `loadBlame`, `loadCompare`, `loadCompareWorking`. Also switched `refreshStagedFiles` and `loadPreviewDiff`.
2. Removed `m.history.ready = false` from cursor-move dispatch sites (delegateKey, clickLeft, scrollLeft). Old content stays visible until the new result arrives. Note: this introduced a stale-title issue (diffFromRev/diffToRev not updated until result arrived), fixed in issue 009 by setting labels eagerly at dispatch time.
3. Added `prefetchAdjacentDiffs`: after loading a diff (cache hit or miss), pre-fetches diffs for cursor±1 with `gen=0`. These results are cached but never displayed (gen=0 always fails the loadGen check). Combined with RunReadOnly, pre-fetches run concurrently with the main load.
