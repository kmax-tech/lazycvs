# 012 — .cvsignore not honored in dirs that aren't added yet

## Problem

A user reorganized files into a fresh subdirectory inside a CVS
working copy. The new subdir contains a local `.cvsignore` covering
the LaTeX build artifacts (`*.aux`, `*.bbl`, `*.blg`, `*.fdb_latexmk`,
`*.fls`, `*.log`, `*.synctex.gz`, ...) plus the editor's own files
(`acl.sty`, `acl_natbib.bst`). In lazycvs every file in the new
subdir was listed with `?` (untracked), the `.cvsignore` patterns
were silently dropped — the right pane showed 35 entries instead of
the handful that actually need to be added.

## Cause

In `tui/app_files.go:189` (top-level files) and 176 (subdir files)
the ignore flag was guarded on an empty status:

```go
ignored := status == "" && matchesIgnore(name, ignorePatterns)
```

`resolveFileStatus()` (tui/app_status.go) returns `?` for any path
whose immediate parent (or any ancestor up to the working-copy root)
has no `CVS/` marker — i.e. for every file in an untracked
directory. That short-circuited the ignore lookup before
`matchesIgnore` ever ran, so the local `.cvsignore` had no effect
on a directory that wasn't yet `cvs add`-ed.

cvs(1) itself would not list these files as `?` — it consults
`.cvsignore` first and drops matching paths. The TUI's behavior
diverged from CVS' precedence.

## Fix

Extend the guard to allow `?` as well:

```go
ignored := (status == "" || status == "?") && matchesIgnore(name, ignorePatterns)
```

Files in a tracked dir keep their existing semantics (real CVS
states M/C/A/R/U are never overridden by ignore patterns). Files
in an untracked dir now pick up the directory's `.cvsignore` and
get the dimmed/strikethrough rendering, are counted as `I` instead
of `?`, and disappear when `hideIgnored` is on — matching CVS'
own behavior and letting the user `A` + `a` only the real sources.
