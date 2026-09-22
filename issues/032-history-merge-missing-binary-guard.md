# 032 — History pair merge lacked the binary-file guard

## Problem

`E` (external diff) is guarded in the key dispatch with `fs.IsBinary`,
but the new `M` (merge the selected revision pair) was not. Pressing `M`
in History on a binary file would extract two revisions and launch a
3-way *text* merge tool on them — producing garbage, with the working
file as the write target.

## Cause

The guard lives in the `E` case's dispatch condition
(`tui/app_keys.go`), so nothing forced the new merge path to repeat it.
Copying the condition onto the `M` case was rejected because a
non-matching case falls through to the viewport — `M` would have become
a silent dead key, the exact failure mode issue 027 fixed for launch
errors.

## Fix

`launchHistoryMergeForCurrent` (`tui/external.go`) checks
`fs.IsBinary` after the pair validation and returns a loud
`externalDiffMsg` error ("… is binary — a text merge tool cannot merge
it") instead of launching the tool.

Regression test: `TestHistoryMergeRejectsBinaryFile` in
`tui/external_test.go` (binary by extension, no on-disk file needed).
