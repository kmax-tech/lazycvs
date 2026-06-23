# 020 — External diff temp filenames hid the revision

## Problem

Launching the external diff tool (e.g. VS Code, meld) on two revisions
of the same file gave each side a temp filename like:

    lazycvs-evaluation-guidelines-1_1-2357731209.md
    lazycvs-evaluation-guidelines-1_2-3100728060.md

In the editor's file tab the revision was buried in the middle of the
name, with a long random suffix dominating the right-hand side. Two
side-by-side tabs looked almost identical and the user couldn't tell at
a glance which pane was 1.1 vs 1.2.

## Cause

`extractRevToTemp` used `os.CreateTemp` with a pattern that put the
basename first, then the rev (with dots replaced by underscores so they
weren't mistaken for an extension), then the `*` random placeholder
before the extension. Filename layout was:

    lazycvs-<basename-no-ext>-<rev>-<random><ext>

`os.CreateTemp`'s random suffix is always present and visually long, and
the basename prefix made every file's name look the same up to the
point where the rev started.

## Fix

Switch to `os.MkdirTemp` so each extraction gets its own unique
directory. With the directory carrying the uniqueness, the filename
itself can be deterministic and short, with the revision in the leading
position so it's the first thing the eye lands on:

    /tmp/lazycvs-diff-<random>/rev-1.1-evaluation-guidelines.md
    /tmp/lazycvs-diff-<random>/rev-1.2-evaluation-guidelines.md

The original extension is preserved unchanged (no underscore-substitution
needed) so syntax-aware diff tools still light up correctly.

## Files touched

- `tui/external.go` — `extractRevToTemp` rewritten to use `MkdirTemp` +
  deterministic `rev-<rev>-<basename>` filename.
