# 004 — Targeted status refresh after actions

## Problem

Three issues with the previous status refresh design:

1. After every action (commit, update, revert, remove, add, ignore), a full recursive `cvs -n -q update -d -P` scanned the entire repo. Slow and unnecessary.

2. Two update mechanisms competed over `statusMap`: the full scan replaced everything, then immediately dispatched `loadDirStatus` for every expanded directory — re-deriving the same data.

3. The startup full scan was a single blocking command. Large repos waited until the entire scan finished before showing any status.

## Architecture (after fix)

### Status refresh flow

Every status scan uses `cvs status -l <dir>` per directory. The only exception is the user-triggered `s` key, which runs a full `cvs -n -q update -d -P` for an authoritative snapshot.

| Trigger | Function | Scope | Parallel |
|---|---|---|---|
| Startup | `backgroundDirScan` via `Init()` | All dirs in repo | Yes — all dirs concurrent |
| Expand directory | `backgroundDirScan` via `treeDirLoadedMsg` | Subtree of expanded dir | Yes — subtree concurrent |
| Tree first load | `treeLoadedMsg` | None — Init's scan covers it | — |
| Post-action (commit, revert, add, etc.) | `refreshStatusForPaths` | Parent dirs of affected files only | Yes — affected dirs concurrent |
| Explicit `s` key | `refreshStatusUser` → `DryRunUpdate` | Entire repo (full replace) | Single command |

### Parallel dir scanning

`loadDirStatus` uses `RunReadOnly` (shared `RWMutex` read lock) instead of `Run` (exclusive write lock). Multiple `cvs status -l` commands run concurrently since they are read-only. Write commands (commit, update, revert) still hold an exclusive lock and block reads.

`backgroundDirScan(executor, epoch, scope)` walks the filesystem from `scope` (or repo root if empty) to find all CVS-managed directories (those containing a `CVS/` subdirectory). It dispatches `loadDirStatus` for each one via `tea.Batch`. Bubble Tea runs all batch commands concurrently, so all directory scans execute in parallel. Results arrive as individual `dirStatusMsg` messages and render progressively.

### Bottom-up count aggregation

Directory change counts (e.g. "3M 1C" next to a folder) are computed bottom-up in `applyStatusToNodes`: each directory's count is the sum of its loaded children's counts plus any `statusMap` entries not covered by a loaded child node. No full-map prefix scans — counts propagate naturally from leaves to root as results arrive.

The file list reads counts directly from tree nodes (`node.Counts`) instead of recomputing them.

### Epoch-based staleness tracking

Every status dispatch increments a monotonic `statusEpoch` counter. Both `statusRefreshedMsg` and `dirStatusMsg` carry the epoch at dispatch time. The `dirStatusMsg` handler only applies results for a directory if `msg.epoch >= m.dirEpoch[dir]`.

This prevents late-arriving results from overwriting fresher data. Example: user navigates to a directory (triggering a subtree scan at epoch N+1) while the startup background scan (epoch N) is still running. The startup scan's result for that directory is dropped because N < N+1.

## Key functions

- **`backgroundDirScan(executor, epoch, scope)`** — walks filesystem for CVS-managed directories under `scope`, dispatches parallel `loadDirStatus` for each.

- **`loadDirStatus(executor, dir, epoch)`** — single `cvs status -l <dir>` via `RunReadOnly`. Returns `dirStatusMsg`.

- **`refreshStatusForPaths(paths)`** — dispatches `loadDirStatus` for each unique parent directory of the given paths. Used after actions.

- **`refreshStatusUser()`** — full `DryRunUpdate` for the explicit `s` key. Returns `statusRefreshedMsg` which replaces the entire `statusMap`.

- **`RunReadOnly`** on `CVSExecutor` — takes `RWMutex.RLock` instead of exclusive lock.

- **`applyStatusToNodes`** — bottom-up tree walk: aggregates children's counts, adds uncovered `statusMap` entries.

## Removed

- `refreshStatus`, `refreshStatusScoped`, `applyStatusByDir`, `countFilesQuick`, `maxFilesForFullScan` — replaced by `backgroundDirScan`.
- `applyOptimisticStatus` — replaced by direct `cvs status` checks.
- `countsByPrefix` — replaced by bottom-up aggregation in `applyStatusToNodes`.
- `CVSExecutor.Update()`, `CVSExecutor.LastUpdate()`, `lastUpdate` field — unused after refactoring.
- `cvs/annotate.go` (`ParseAnnotate`, `AnnotateLine`) — unused.
- `FormatFileSize`, `MatchErrorPattern`, `ErrorPatterns` — unused.
