# 031 — B (restore backup) was a dead key in the History tab

## Problem

The History pair merge (M) writes a `.lazycvs-backup` before handing the
merge tool write access to the working copy, and the USER_GUIDE promised
"`B` restores it". But pressing `B` in the History tab did nothing — the
key fell through to the right-pane viewport and scrolled a no-op. The
user had to leave the tab to undo a merge started inside it.

## Cause

The `RestoreBackup` case in `delegateKey` (`tui/app_keys.go`) was gated
with `m.activeTab != TabHistory`. The gate existed because the general
restore flow reads the file panels' selection (marked set, tree subtree)
— state the History tab doesn't display, so following it there could
have restored files the user couldn't see.

## Fix

A History-specific case before the general one: in History, `B` restores
only the loaded file's backup (`m.history.Path()`), batched with
`invalidateHistoryCache` so the vs-working pane reflects the restored
content. The marked-set and subtree flows stay panel-only. The History
section of the help dialog gained a `B` row.

Regression test: `TestRestoreBackupWorksInHistoryTab` in
`tui/app_keys_test.go` presses `B` via `delegateKey` and asserts the
backup content lands in the working file.
