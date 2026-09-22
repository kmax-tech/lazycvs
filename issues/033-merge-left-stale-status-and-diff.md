# 033 — External merge left status and vs-working diff stale

## Problem

After the History pair merge (or the conflict merge) wrote into the
working copy via the external tool, nothing refreshed: the tree kept
showing the file as unmodified, `w` served the cached pre-merge diff,
and the success note's "then commit" was not actionable until the user
manually pressed `s`.

## Cause

`externalDiffMsg` carried only `note` and `err`; the handler in
`app_core.go` set the result bar and stopped. The `B`-in-History case
added in the same change did invalidate the History cache — the merge
path just never grew the same follow-up.

## Fix

`externalDiffMsg` gained `refreshPath`; both merge flows set it to the
file the tool writes into, threaded through `launchExternalMerge` and
`spawnTool`. The handler batches `refreshStatusForPaths` and
`invalidateHistoryCache` for it. For terminal tools the message arrives
after the tool exits, so the refresh sees the result; for detached GUI
tools it arrives at launch time, where it at least drops the stale
cached diff (the tool writes later — the next status refresh picks the
change up).

Test: `TestExternalDiffMsgRefreshesMergedPath` in `tui/external_test.go`
asserts the status epoch advances and the cached diff for the merged
file is gone.
