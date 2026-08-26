# 028 — demo.py aborted: unscoped commit walked into the demo conflict

## Problem

`make demo` failed while generating the demo working copy:

```
cvs commit: file `docs/notes.txt' had a conflict and has not been modified
cvs [commit aborted]: correct above errors first!
```

## Cause

The stale-directory step committed the new `legacy/` module with
`cvs commit -m "add legacy module"` **without a path argument**. An
unscoped commit recurses over the whole working copy — including
`docs/notes.txt`, which an earlier step had deliberately put into
conflict (C status) for the demo. CVS refuses to commit a conflicted
file that hasn't been touched since the merge and aborts the entire
commit.

## Fix

Scope the commit to the directory it is about:
`cvs commit -m "add legacy module" legacy`. All other commits in the
script were already path-scoped (or run in a clean parallel WC).
