# 019 — KNOWN ISSUE: dirEpoch only bumps existing keys on full refresh

Status: **open** — documented edge case, not yet fixed. Revisit if a
user reports stale status on freshly created directories.

## Symptom (theoretical — not yet observed)

A directory created *during* a running session could briefly show
stale status after this sequence:

1. User creates a new dir `newdir/` + runs `cvs add newdir`.
2. An action inside it triggers a **targeted** refresh
   (`refreshStatusForPaths` → `loadDirStatus("newdir", epoch=41)`).
   That scan is slow (server hiccup) and stays in flight.
3. Meanwhile the user presses `s` → `statusRefreshedMsg` with
   `epoch=42` lands first and rebuilds `statusMap` with fresh data.
4. The slow targeted result from step 2 (epoch 41) finally arrives.

Expected: the stale epoch-41 result is discarded.
Actual: it is **applied**, overwriting the fresher epoch-42 state
for `newdir/`.

## Cause

The guard in the `dirStatusMsg` handler (tui/app_core.go:~575)
discards stale results via:

```go
if msg.epoch < m.dirEpoch[msg.dir] { return m, nil }
```

`statusRefreshedMsg` (the `s` refresh) bumps the epoch for every
**known** dir:

```go
for dir := range m.dirEpoch {
    m.dirEpoch[dir] = msg.epoch
}
```

A dir that has never had a targeted scan completed is not a key in
`m.dirEpoch` yet, so the bump misses it. When the slow targeted
result arrives, `m.dirEpoch["newdir"]` reads as the zero value 0,
the `msg.epoch < 0` check is false, and the stale result passes.

## Why it's been left open

- Requires a precise race: targeted scan dispatched before the full
  refresh, completing after it, on a dir whose first-ever scan it is.
- Self-healing: the next `s` / tab-switch refresh (epoch N+1)
  overwrites the stale data.
- The clean fix touches bookkeeping shared by four refresh paths
  (M effort) and wasn't worth the risk while the status pipeline
  was being reworked for the stdout/stderr-merge bug (issue 018).

## Sketch of the fix (when picked up)

Replace the per-dir bump loop with a single watermark:

```go
// App state
lastFullRefreshEpoch uint64

// statusRefreshedMsg handler
m.lastFullRefreshEpoch = msg.epoch   // instead of the range loop

// dirStatusMsg guard
if msg.epoch < m.dirEpoch[msg.dir] || msg.epoch < m.lastFullRefreshEpoch {
    return m, nil
}
```

This makes the "full refresh supersedes anything older" rule hold
for dirs that have never been individually scanned, removes the
O(n) map walk, and keeps the per-dir epoch for targeted-vs-targeted
ordering. Remember to also drop the subtree bump loop in the
recursive `dirStatusMsg` branch (app_core.go:~596) in favor of the
same watermark, and to delete `dirEpoch` entries when a scan shows
a dir no longer exists.
