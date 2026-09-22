# 034 — Pair merge proceeded when the safety backup failed

## Problem

`writeBackupFile`'s return value only gated the Console log line. If the
backup write failed (read-only directory, full disk), the History pair
merge handed the external tool write access to the working copy with no
safety copy — silently, while the USER_GUIDE promises "A
`.lazycvs-backup` copy is written before the tool starts".

## Cause

The backup call was copied from flows where a `false` usually means
"file missing on disk, nothing to protect" and is harmless. In the merge
flow the file demonstrably exists — `copyWorkingToTemp` read it one step
earlier — so `false` can only be a write failure, which deserves an
abort, not a shrug.

## Fix

`launchHistoryMergeForCurrent` (`tui/external.go`) now aborts with a
loud `externalDiffMsg` error ("could not write …lazycvs-backup — merge
aborted to keep an undo copy") when `writeBackupFile` returns false.

No unit test: forcing the failure needs a working `cvs` binary in the
fixture (the extraction steps run first), which the tui tests don't
have. The guard is two lines directly above the launch.
