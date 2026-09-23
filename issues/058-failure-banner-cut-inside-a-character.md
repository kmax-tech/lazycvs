# 058 — The failure banner cut its reason inside a character

## Problem

`actionFailure` (issue 057) cuts a long cvs error at 80 characters so
the banner fits. It cut bytes, not characters: `reason[:77]`. A reason
naming `docs/…Größe.txt` — a file the demo now carries — can land the
cut between the two bytes of the `ö`, and the banner carries invalid
UTF-8:

```
path escapes working copy: docs/xxxx…Grö\xc3…
```

Found reviewing the commit, before anyone saw it on screen.

## Cause

Byte slicing on a string that is not ASCII. The rest of the TUI
measures in display cells (`padCells`, `truncateRow`, the console
footer) through `ansi.Truncate`; this one place did not.

## Fix

`ansi.Truncate(reason, 80, "…")` — cells, and never inside a
character. The test feeds a reason whose 77th byte is the middle of an
`ö` and checks the result is valid UTF-8.

Two things picked up on the way:

- `pathsLabel` names one file or counts several, replacing three
  hand-written copies of the same `if len == 1` (one of which still
  said `%d path(s)`), and the commit and update failure banners now
  carry the reason like remove and add/revert do (057 claimed they
  already did).
- The Marked tab's commit block said `commit (1 files)` — the one
  count 134282e missed.

## Files touched

- `tui/app_core.go`, `tui/marked.go`, `tui/header_test.go`,
  `issues/057-refusals-pointed-at-an-empty-console.md`
