# 013 — `s` refresh produced two frames with rendering glitches

## Problem

Pressing `s` on a working copy with many directories produced visible
artifacts: doubled `Tree`/`Files` headers, the same row rendered twice
with and without its `! stale` marker, a raw `[34m` ANSI escape leaking
into the tree column. Resizing or scrolling temporarily cleared it,
the next `s` brought it back.

A secondary symptom: after `s` the History tab's `(working)` badge on
sticky-tag checkouts was stale until the next `u`/init scan.

## Cause

Two separate things ran for one logical "give me the current state"
request, but they shipped different pieces of the picture and they
were not coordinated:

- `refreshStatusUser` ran `cvs -n update` — knows server-side `U`
  pending, conflicts, untracked, and `StaleDirs`. No `WorkingRev`.
- `backgroundDirScan` ran the per-partition `cvs status` worker pool
  — knows the per-file `WorkingRev` (sticky-tag aware, used by the
  History (working) badge). No `StaleDirs`.

`s` only triggered `refreshStatusUser`. So `baseRevMap` was never
refreshed by the user's "refresh" key. And because the dry-run update
takes ~1–2 s on a big repo, the user usually triggered other state
mutations during the wait — those rendered ahead of the dry-run
result. By the time the dry-run landed and the notification banner
appeared (shifting the layout by one line), the terminal's diff
renderer didn't fully overwrite the previous frame, leaving header
duplicates and a half-rendered ANSI sequence visible.

A subsequent investigation found another problem with the "fix":
the parallelism was defeated by the executor's RWMutex. `Run`
takes an exclusive `Lock`, `RunReadOnly` takes a shared `RLock`.
`DryRunUpdate` used `Run`, so it blocked the worker-pool goroutines
that used `RunReadOnly`. The two paths still serialized despite
running in goroutines.

## Fix

Two-step:

1. **Atomic refresh** — extracted the worker-pool body into
   `runPartitionScan`, then made `refreshStatusUser` fan-out both
   the dry-run and the partition scan as parallel goroutines, wait
   for both, and return a single `statusRefreshedMsg{result, statuses}`.
   The handler rebuilds `statusMap` from the dry-run and overlays
   `baseRevMap` from the scan — one View() render, complete data.

2. **Actual parallelism** — switched `DryRunUpdate` from `Run` to
   `RunReadOnly` (`cvs -n update` is read-only). Now both goroutines
   share the `RLock` and run concurrently. Total wait equals the
   slower of the two instead of their sum.

Defensive: the handler only resets `baseRevMap` when `len(statuses) > 0`,
so a failed/timed-out scan can't wipe the prior sticky-tag map.
