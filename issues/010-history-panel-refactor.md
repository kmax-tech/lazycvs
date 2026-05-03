# 010 — History panel: per-file caches, deduped header, dead-compare cleanup

## Problem

Several recurring symptoms in the History tab — header sometimes doubled,
sometimes missing; right-pane "Loading…" flashes on revisit; reloading
revisions / diffs every time the user switches files; intermittent wrong
diff after fast file switching. Issues 003, 005, 007, 008, 009 each fixed
a real symptom but the underlying coupling kept producing new variants.

## Cause

The model collapsed three concerns into one struct (`HistoryModel`):

1. **Data**: `revisions`, `diffData`, `content`, `diffCache`, `contentCache`
2. **UI state**: `cursor`, `offset`, `mode`, `viewport`, `diffSBS`
3. **Display**: rendered header inside `ViewLeft` while the panel frame
   *also* rendered "Revisions" — two title lines for one panel.

Concrete bugs that fell out of this:

- `historyDiffMsg` / `historyContentMsg` wrote into the cache **before**
  the gen check. If the user switched files mid-flight, a result for file
  A was stored under file B's cache (same `"from:to"` key shape). On the
  next visit to that revision pair in B, the wrong diff would render.
- `historyLoadedMsg` wiped both caches on every file load, so revisit was
  always a full re-fetch — opposite of the "load once" intent.
- `m.ready = false` was reset on every file load, producing a placeholder
  flash even when the new file's data was already cached.
- `m.path = msg.path` was unguarded — an empty `loadHistory` dispatch
  could leave `m.path == ""`, and `ViewLeft` returned "No file selected"
  instead of preserving the previous header.
- The mode (`Diff`/`Content`/`Blame`) was reset to `Diff` on every load,
  losing the user's selected view.
- Half-removed `compare*` infrastructure (six fields, dead key handlers,
  a renderer) cluttered the model and the keymap.

## Fix

Three concerns split into three layers:

**1. App-owned per-file caches (never wiped on file switch)**

```go
// in App
histRevisions map[string]*cvs.FileHistory
histDiffs     map[string]map[string]*cvs.DiffResult  // path → "from:to" → diff
histContents  map[string]map[string]string           // path → rev → content
histPending   map[string]bool                        // dedup in-flight loads
```

Async results carry the `path` they were loaded for. The handler in
`app_core.go` writes to `histRevisions[msg.path]` regardless of which
file the user is currently viewing, then *only* pushes data into the
HistoryModel if `m.history.Path() == msg.path` and the cursor still
matches the result's revision key. Cache poisoning is impossible by
construction — each entry lives under its source path.

**2. Slim HistoryModel (view + UI state only)**

The model now holds only `path`, `cursor`, `offset`, `mode`, `diffSBS`,
the viewport, and the data currently projected for rendering. Caches,
`ready`, `hasWorkingCopy`, `loadGen`, and all `compare*` fields are gone.
`SwitchTo(path)` saves UI state for the outgoing file in a `uiState`
map and restores it for the incoming one — so navigating A → B → A
keeps the user's cursor and mode.

**3. Single header**

`ViewLeft` no longer renders a header line. The panel frame title in
`app_layout.go:235` carries the file name: `Revisions — path/to/file`.
One title, owned by one place — can't get out of sync, can't double up,
can't disappear independently of the panel.

## Other cleanups

- Removed `loadWorkingDiff`, `loadWorkingContent`, `loadCompare`,
  `loadCompareWorking`, `historyCompareMsg`, `HistoryCompare` mode and
  every `compare*` field — the half-disabled feature is gone, not just
  commented out.
- Removed `hasWorkingCopy` pseudo-row (was always false).
- Removed `loadGen` — path identity is now what gates display updates,
  and `histPending` deduplicates in-flight requests.
- Click and scroll handlers updated to the new "no inline header,
  2 lines per row" geometry (`row/2 + offset`, was `row - 1`).
- `ensureVisible` and `ViewLeft` now agree on `visibleCount = m.height/2`.

## Result

- Switching files A → B → A re-uses A's cached revisions and diffs (no
  cvs commands re-run).
- Within a file, neighbor diffs are still pre-fetched on cursor move and
  served instantly on revisit.
- The header is always present when a file is open — no conditional
  hide path that can be reached.
- The right-pane title (`Diff — 1.20 ↔ 1.21`) is set eagerly on cursor
  move (via `SetPendingLabels`) so it always matches the cursor, never
  the previous async result.
