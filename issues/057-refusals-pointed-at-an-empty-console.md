# 057 — A refused action pointed at a console that said nothing

## Problem

`D` on a symlink pointing outside the working copy is correctly
refused: `ValidatePath` rejects the escape and the file outside is
never touched. (Verified by hand: the target survived.)

But the user is told

```
✗ Remove failed for escape-link.txt — see Console
```

and the console holds four successful `cvs status` lines and nothing
else. The refusal happened *before* any cvs command ran, so nothing was
logged — the banner pointed at an empty room.

The guarantee held; the explanation did not. A user seeing this learns
only that the app will not do what they asked.

## Fix

Two halves:

- **The refusal is logged.** `validatePaths` records it through
  `CommandLog.LogFileOp`, the same channel lazycvs already uses for the
  file operations it performs itself (`rm`, `mv`, backup restore). The
  console now shows `refused escape-link.txt` with the reason beneath
  it, and the failure banner's `(C-j: console, errors only)` hint
  appears, because there is now an unread error to read.
- **The banner carries the reason** instead of pointing anywhere:
  `✗ Remove failed: path escapes working copy: escape-link.txt`.
  `actionFailure` takes the error's first line, cuts it at 80
  characters (the console has the full text), and drops the file label
  when the reason already names the file.

Both failure handlers — remove and the shared add/revert path — use it.

## Files touched

- `tui/app_actions.go` (`validatePaths` logs), `tui/app_core.go`
  (`actionFailure`, both handlers), `tui/header_test.go`
