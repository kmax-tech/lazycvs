# History Tab Scroll Rendering — Bug Analysis & Architecture Guide

This document covers the full chain of rendering bugs encountered in the History tab's revision scrolling, why they're interconnected, and what the final architecture looks like. Intended as a reference for future debugging.

## Why this is hard

The History tab combines four things that create a perfect storm for rendering bugs:

1. **Async data loading**: CVS commands run in background goroutines. Results arrive later via Bubble Tea messages. The UI must decide what to show between dispatch and result.
2. **Value-receiver semantics**: `HistoryModel.Update()` is a value receiver — it works on a copy. The caller must assign the return value back. Meanwhile, `loadHistoryContent()` modifies `m.history` directly through `*App`. These two mutation paths must stay consistent.
3. **Dual-panel rendering**: The left panel (revision list) and right panel (diff/content viewport) render independently from the same model state. The panel title is computed in `renderMainContent()` from model fields, not from the viewport content itself.
4. **Multiple entry points**: The same `loadHistoryContent()` can be triggered by keyboard navigation, mouse click, mouse scroll, mode change, initial file load, or Enter key. Each path has slightly different pre/post conditions.

## The rendering pipeline

```
User input (key/mouse)
    │
    ▼
delegateKey() / handleMouse()
    │  ← moves cursor, checks if cursor/mode changed
    ▼
loadHistoryContent()
    │  ← sets diffFromRev/diffToRev (title)
    │  ← checks cache → hit: applyDiff() synchronously, return nil
    │  ← cache miss: return async tea.Cmd + prefetch cmds
    ▼
[async goroutine runs CVS command via RunReadOnly]
    │
    ▼
historyDiffMsg / historyContentMsg arrives
    │  ← cache result (even if stale)
    │  ← check loadGen → stale: drop silently
    │  ← matching gen: set diffFromRev/diffToRev, applyDiff()
    ▼
View() → renderMainContent()
    │  ← reads diffFromRev/diffToRev for panel title
    │  ← calls ViewRight() → viewport.View() or placeholder
    ▼
renderPanel() wraps content in borders with title
```

## Key state fields and their roles

| Field | Set by | Read by | Purpose |
|-------|--------|---------|---------|
| `loadGen` | `loadHistoryContent()` (incremented) | Result handlers (gen check) | Monotonic counter; stale results (gen mismatch) are cached but not displayed |
| `ready` | `historyLoadedMsg` (false), `applyDiff`/`applyContent` (true) | `ViewRight()` | Controls whether viewport or placeholder is shown |
| `diffFromRev`, `diffToRev` | `loadHistoryContent()` (eager), result handlers (redundant) | `renderMainContent()` | Build the right panel border title "Diff — X ↔ Y" |
| `diffCache` | Result handlers (before gen check) | `loadHistoryContent()` | Avoids re-running CVS commands for previously visited revisions |
| `viewport` | `applyDiff()`/`applyContent()` (new viewport created) | `ViewRight()` → `viewport.View()` | Bubble Tea viewport holding rendered diff/content |
| `cursor`, `offset` | Key/mouse handlers | `ViewLeft()`, `loadHistoryContent()` | Current selection and scroll position in revision list |

## The bug chain (issues 003–009)

Each fix solved a real symptom but exposed the next timing window:

### 003 — Stale diff content (title updated, content lagged)
**Problem**: `loadHistoryContent()` set `diffFromRev`/`diffToRev` synchronously but the diff loaded async. The viewport showed old content while the title already reflected the new revision.
**Fix**: Set `ready = false` on dispatch → show "Loading diff..." placeholder instead of stale content.
**Residual**: Placeholder flashed on every cursor move.

### 005 — Out-of-order async results
**Problem**: Fast scrolling dispatched many CVS commands. Results arrived out of order, flashing intermediate diffs before the final one. `ready = false` showed "Loading..." correctly, but late results overwrote the viewport with wrong content.
**Fix**: `loadGen` monotonic counter. Stale results (gen mismatch) silently dropped.
**Residual**: Still flashed "Loading..." on every move because `ready = false` was set eagerly.

### 006 — `d` key reloads entire history
**Problem**: Pressing `d` while already in the History tab hit the global file-action handler instead of `HistoryModel.Update()`, causing a full `cvs log` reload and duplicate commands.
**Fix**: Guard `keys.Diff` handler with `activeTab != TabHistory`.
**Residual**: Solved cleanly. Also explains the "duplicate commands in console" observation.

### 007 — Ghost highlighted rows (ANSI state corruption)
**Problem**: Selected revision row had nested ANSI styles: outer `Reverse(true)` + inner `mutedStyle.Render()`, `colorUpdated`, etc. Inner styles' `\x1b[0m` resets cancelled the outer reverse, leaving fragments on screen.
**Fix**: Strip all inner ANSI before applying Reverse; pad to full width inside the reverse wrapper.
**Residual**: Solved cleanly.

### 008 — Placeholder flash + mutex serialization
**Problem**: Every cursor move flashed "Loading diff..." because `ready = false` was set eagerly. All read-only CVS operations used the exclusive write mutex, serializing everything including stale pre-empted loads.
**Fix**: (a) Removed `ready = false` from cursor moves — old content stays visible. (b) Switched all read-only operations to `RunReadOnly` (shared RLock). (c) Added pre-fetch for cursor±1 with `gen=0`.
**Residual**: Title showed stale revision labels because `diffFromRev`/`diffToRev` were only set in the result handler.

### 009 — Title shows stale/missing revision labels
**Problem**: With `ready` staying true (008), old content + old title showed briefly. Also, `historyLoadedMsg` never cleared `diffFromRev`/`diffToRev`, so switching files kept the old file's labels.
**Fix**: (a) Clear labels in `historyLoadedMsg`. (b) Set labels eagerly in `loadHistoryContent()` at dispatch time, before cache check. Title always reflects current cursor position.

## Current architecture (post-009)

### Dispatch flow (`loadHistoryContent`)

```
1. Increment loadGen
2. Set diffFromRev/diffToRev to current revision pair  ← EAGER (title always current)
3. Check cache
   ├─ HIT:  applyDiff() synchronously, return prefetchAdjacentDiffs()
   └─ MISS: return withPrefetch(loadRevisionDiff(..., gen), path)
            (old viewport stays visible until result arrives — no ready=false)
```

### Result flow (catch-all → `history.Update`)

```
1. Cache the result (even if stale — cache key is rev pair, not gen)
2. Check gen:
   ├─ STALE (gen != loadGen): return immediately (result is cached for later)
   └─ MATCH: set diffFromRev/diffToRev (redundant but safe), applyDiff()
```

### Concurrency model

```
RunReadOnly (RLock)          Run (exclusive Lock)
─────────────────            ────────────────────
cvs log                      cvs add
cvs diff -u -r X -r Y        cvs commit
cvs annotate                 cvs update -C
cvs co -p -r X               cvs remove -f
cvs status (dir scan)
cvs status (staged refresh)
cvs diff -u (preview)
```

Read-only operations run concurrently (shared lock). Pre-fetch diffs for cursor±1 run alongside the main diff without blocking.

### Pre-fetch mechanism

After dispatching the main diff (or on cache hit), `prefetchAdjacentDiffs()` dispatches diffs for cursor-1 and cursor+1 with `gen=0`:
- `gen=0` always fails the `loadGen` check → never displayed
- But caching happens BEFORE the gen check → result stored in `diffCache`
- Next cursor move to that revision hits cache → instant `applyDiff()`
- Uses `RunReadOnly` → runs concurrently, doesn't block main load

### What "ready" means now

- `false` ONLY during initial file load (set by `historyLoadedMsg`)
- `true` once first diff/content arrives (set by `applyDiff`/`applyContent`)
- NEVER set back to false during cursor moves
- Controls whether `ViewRight()` returns placeholder or viewport content

## Debugging checklist for future rendering issues

1. **Stale content?** Check that `loadGen` is incremented and carried correctly in the dispatched message. Check that the result handler drops stale results.

2. **Wrong title?** Check that `diffFromRev`/`diffToRev` are set in `loadHistoryContent()` at dispatch time (eager) AND cleared in `historyLoadedMsg` (file change).

3. **Duplicate commands in console?** Check for code paths that call `loadHistoryContent()` multiple times per event cycle. Check issue 006 pattern: global key handlers intercepting keys meant for `HistoryModel.Update()`.

4. **Panel height wrong?** Verify `overhead` constant in `layout()` matches actual rendered lines (tabbar + keybar + 2×borders + 2×padding). Check that `contentH` + `consoleH` + overhead == `m.height`.

5. **Ghost highlights / ANSI corruption?** Never nest lipgloss `Render()` inside another `Render()` — inner resets cancel outer styles. Strip ANSI first, then apply outer style.

6. **Viewport dimensions mismatch?** `SetSize()` only updates viewport dimensions when `ready == true`. If ready is false during a resize, the viewport keeps old dimensions until the next `applyDiff()` creates a new one with current `m.height`.

## Files involved

- `tui/history.go` — Model, Update, ViewLeft, ViewRight, load functions, cache
- `tui/app_helpers.go` — `loadHistoryContent()`, prefetch, mouse handlers
- `tui/app_keys.go` — Keyboard dispatch for TabHistory (delegateKey)
- `tui/app_core.go` — Message routing: explicit cases vs catch-all
- `tui/app_layout.go` — `renderMainContent()`, `renderPanel()`, `layout()`
- `cvs/executor.go` — `Run` (exclusive) vs `RunReadOnly` (shared)
- `cvs/commandlog.go` — Ring buffer (one `Add()` per `run()` call)
