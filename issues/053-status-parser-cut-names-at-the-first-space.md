# 053 — Files with spaces in their names read as clean

## Problem

`docs/Zusammenfassung Größe.txt` had local changes. `cvs -n update`
reported `M`, and the flat listing showed it. Open the directory and
the file turned clean: no status badge in the tree, no `c` or `r` in
the keybar, and the diff pane said **"No local changes"** about a file
that had them.

A file the user cannot see as modified is a file the user does not
commit.

## Cause

`cvs status` prints

```
File: Zusammenfassung Größe.txt<TAB>Status: Locally Modified
```

and the parser captured the name with `(\S+)`, which stops at the first
space. The status was therefore recorded under the path
`docs/Zusammenfassung`, which matches no file, and the real path kept
whatever the per-directory scan left behind: nothing.

The separator CVS uses is a tab, so a name with spaces is perfectly
unambiguous — the regex just was not written for it.

## Fix

Capture the name non-greedily up to the separator:

```go
^File:\s+(?:no file\s+)?(.+?)\s+Status:\s+(.+)$
```

The "Examining <dir>" line was already parsed by splitting on the
literal, so directories with spaces were never affected; a test now
pins that too.

Tests cover: plain names, one space, several spaces, the `no file`
prefix with and without spaces, tab and space separators, and a
directory whose name contains a space.

## Files touched

- `cvs/status.go`, `cvs/status_test.go`
