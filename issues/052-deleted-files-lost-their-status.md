# 052 — A file you delete by hand disappeared from the listing

## Problem

Delete a tracked file in the shell (`rm src/util.go`) and refresh:
`cvs -n update` reports `U src/util.go`, the tree badge shows `1U`, and
the file list shows the row. Then open that directory — and the row is
gone, the `U` badge with it. The pending "your copy is missing, update
will bring it back" state vanished from the UI, silently.

## Cause

Opening a directory runs a per-directory `cvs status`, whose result
overwrites the dry-run's knowledge for that directory. For a missing
file CVS says:

```
File: no file util.go		Status: Needs Checkout
```

`cvsStatusCode` had no case for **"Needs Checkout"** — it knew "Needs
Update" and "Needs Patch" — so it returned "", the file was recorded
as having no status, and an entry with no status and no file on disk
is not listed.

The phrase was not unknown to the codebase: `fs/compare.go` had its own
table of CVS status phrases, and *that* one knew "Needs Checkout". Two
tables for one vocabulary, drifted — the same fault issue 024
catalogued, with the drift now visible as a disappearing file.

## Fix

`cvs.ClassifyStatus` is the one place that knows what CVS's phrases
mean, returning a `StatusKind`. The two consumers project it:

- `StatusKind.StatusCode()` — the listing's one-letter alphabet, the
  same one `cvs update -n` prints
- `fs.compareLabel` — the compare view's own vocabulary

"Needs Checkout", "Needs Patch" and "Needs Update" all classify as
`StatusNeedsUpdate`, which is the truth for all three: the server has
content this working copy does not, and `u` fixes it.

## Files touched

- new `cvs/statuskind.go`
- `tui/app_status.go` (`cvsStatusCode` derives), `fs/compare.go`
  (`compareLabel` derives)
