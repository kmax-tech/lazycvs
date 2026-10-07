# 060 — Revert never undid a `cvs remove`

## Problem

Remove a file (`D`), then revert it (`r`): the file stays gone. Two
shapes, both stuck:

- **R** (server unchanged): `cvs update -C f` exits 0, prints `R f`,
  and changes nothing. The revert reports success; the row stays R.
- **C** (someone committed a newer revision after the remove): the
  status scan reports
  `conflict: removed 'f' was modified by second party`, so the row is
  C. Revert takes the C path, `rm f` + `cvs update f`, which fails with
  the same conflict message and exit 1. The C never goes away.

## Cause

`cvs remove` marks the file in `CVS/Entries` by prefixing its revision
with `-` (`/f/-1.3/…`). Neither `update` nor `update -C` undoes that
mark; only `cvs add f` does ("version 1.3, resurrected"), which puts
the last checked-out revision back on disk and clears the `-`.

## Fix

`revertOne` (`tui/app_actions.go`) checks Entries first
(`entryRemoved` in `tui/app_files.go`). For a removed file it runs
`cvs add`, then the normal `cvs update -C`, which fetches the server's
current revision on top of the resurrected one. The R/C status passed
in no longer matters for that file.

`TestRevertOneUndoesRemove` drives both shapes against a real local
repository (skipped without a `cvs` binary); it fails on the old code.
