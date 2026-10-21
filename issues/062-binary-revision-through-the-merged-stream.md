# 062 — A binary revision travelled through the merged stdout/stderr stream

## Problem

Every revision lazycvs pulls out of the repository (`cvs co -p`, used by
the History content pane and by the temp files for the external diff
and merge tools) came back through `RunReadOnly`, whose stdout and stderr
share one buffer (see `cvs/executor.go`: `cvs status` needs the
interleaving). The "Checking out" header that `cvs co -p` prints to
stderr therefore landed in front of the content and was cut off again by
a regex (`stripCheckoutHeader`). For text that worked. For a PDF or an
image it was a gamble: any stderr line cvs emits mid-stream (a server
warning, a lock notice) ends up inside the bytes, and the header stripper
scanned the first lines of binary data for asterisks. There was also no
way to show an old revision of a binary at all — the History pane says
"cannot display", and `o` / `Ctrl-o` always opened the working copy.

A second gap: a revision the file never had is not an error to cvs
(`co -p -r 9.9` exits 0 with no output), and the old code returned an
empty string as if that were the content.

## Cause

One executor entry point for two different kinds of output — parser
input, where the interleaving matters, and file content, where it must
never happen.

## Fix

- `RunReadOnlyStdout` (`cvs/executor.go`): stdout is captured on its
  own and returned as bytes; the logged result carries stderr plus a
  one-line size note, so the Console never renders binary data.
- `catRevision` (`tui/external.go`) uses it and returns bytes; the
  header stripper and its test are gone. No bytes and no "Checking out"
  header on stderr means the revision does not exist, reported as an
  error.
- `ParseLog` records the `keyword substitution:` mode; a binary stored
  without `-kb` is opened anyway but flagged in red.
- History tab: `o` / `Ctrl-o` open the revision under the cursor via the
  existing `extractRevToTemp`; the working-copy row opens the file.

`TestCatRevisionReturnsExactBinaryBytes` drives a real local repository
with a `-kb` file containing a NUL, CRLF, a `$Id$` and a 0xFF byte and
compares the bytes of two revisions and HEAD; it also asserts the
missing-revision error and the size note in the log.
