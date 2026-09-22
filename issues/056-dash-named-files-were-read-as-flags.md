# 056 — A file named like a flag was read as one

## Problem

`a` on `-dashfile.txt` did nothing. The console showed:

```
add: illegal option -- d
Usage: cvs add [-k rcs-kflag] [-m message] files...
```

The app ran `cvs add -dashfile.txt`, and cvs read the file name as
options. The same applies to every command that takes a path — commit,
diff, remove, update, log, status — so a file whose name begins with a
dash is invisible to every action in the app.

## Cause

Nothing translated "this argument is a path" into something cvs would
read as a path. Twenty call sites build paths into argument lists; each
one passed the path through untouched.

## Fix

The executor rewrites such arguments on the way out: an argument that
starts with `-`, is longer than a short flag, and **exists in the
working copy** becomes `./<name>`, which cvs reads as the path it is.

The narrowness is deliberate. Every flag lazycvs passes is one or two
characters (`-u`, `-r`, `-kb`, `-dP`), and none of them names a file,
so a real flag cannot be caught by the rule. A working copy holding a
file called exactly `-u` would still confuse cvs — and confuses it from
a shell too.

It lives in the executor rather than at the twenty call sites because a
rule that must be remembered separately at each of them is a rule that
will be forgotten at the twenty-first. (The same argument as issue 024's
`validatePaths`.)

## Files touched

- `cvs/executor.go`, `cvs/executor_validate_test.go`
