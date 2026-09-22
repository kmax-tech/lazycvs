# 035 — diffuse merge preset had no $MERGED, merges silently went nowhere

## Problem

With `merge_tool = "diffuse"`, `M` wrote the `.lazycvs-backup`, showed
the green "save in the tool, then commit" note — and the merge could
never land: the preset `"diffuse $LOCAL $BASE $REMOTE"` never passes the
working file to the tool. The user got a success message and a stray
backup for a merge that cannot happen.

## Cause

diffuse has no output-file flag, so its preset simply omitted `$MERGED`.
Nothing validated that a merge template can actually deliver a result;
the placeholder table was convention, not contract.

## Fix

- The `diffuse` entry is removed from `config.MergeToolPresets` (it
  stays a valid *diff* preset).
- `launchExternalMerge` rejects any template lacking `$MERGED` with a
  loud error — this also catches custom `merge_command` values.
- Preset listings in README, USER_GUIDE, and the no-merge-tool dialog
  now separate the diff and merge preset sets.

Tests: `TestLaunchExternalMergeRejectsTemplateWithoutMerged` and
`TestMergePresetsAllCarryMerged` in `tui/external_test.go` — the second
pins the contract so a future preset cannot reintroduce the trap.
