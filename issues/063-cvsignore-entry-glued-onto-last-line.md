# 063 — An appended ignore entry glued onto the last line of .cvsignore

## Problem

`~/.cvsignore` contained the line `*.iml.DS_Store` followed by
`.DS_Store`. Neither `*.iml` files nor (until the second line appeared)
`.DS_Store` were ignored. The file had been written by hand and ended in
`*.iml` without a trailing newline; the startup sync of
`ignore.global_patterns` appended `.DS_Store\n` straight after it. On the
next start the sync did not find `.DS_Store` as an entry and appended it
again, which produced the second line.

The same gap existed in every other place lazycvs appends to an ignore
file: the `i` dialog (local file, global `*.ext`, global exact name) and
the bulk ignore of marked files. A hand-written `.cvsignore` in a working
copy without a final newline would have swallowed the first name ignored
from the TUI.

## Cause

Four copies of "open with `O_APPEND`, write `entry + "\n"`", none of
which looked at the last byte already in the file. The sync also split
the existing file on newlines, while cvs(5) separates entries by any
whitespace.

## Fix

- `fs.AppendIgnoreEntries` (`fs/cvsignore.go`) is the one writer: it
  reads the file's last byte and writes a newline first when it is
  missing, then one entry per line. It returns the close error as well.
- `config.SyncGlobalIgnore`, `handleIgnore`, `appendToGlobalCvsignore`
  and `markedBulkIgnore` call it instead of opening the file themselves.
- `SyncGlobalIgnore` collects existing entries with `strings.Fields`.

`TestAppendIgnoreEntries` covers the missing file, the empty file, a
file with and without a trailing newline, and an empty entry list.
Files already damaged are not repaired; the glued line has to be split
by hand.
