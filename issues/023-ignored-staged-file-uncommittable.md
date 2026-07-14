# 023 — Staged `?` file could not be committed (silent no-op)

## Problem

A staged file showed status `?` in the Staged tab, but pressing `c`
(or Enter in the commit input) did nothing — no dialog, no banner, no
cvs invocation in the console. The file matched a `.cvsignore`
pattern (`*frame.pdf`), which turned out to be the trigger.

## Cause

Several layered issues:

1. **Resolver drift.** Ignored files are never reported by the
   `cvs -n update` dry-run, so `statusMap` has no entry. The file
   *listing* still shows `?` via the CVS/Entries promotion in
   `resolveListingStatus`, but `resolveFileStatus` (used by the
   Staged tab and every commit gate) lacked that promotion and
   returned `""` (clean) whenever all ancestors carried CVS/
   metadata. List says `?`, gate says "nothing committable".
2. **Silent gates.** Both the Staged-tab `c` handler and the commit
   input's Enter handler returned without any feedback when
   `PathsByStatus` came back empty — indistinguishable from a broken
   key.
3. **Focus dependence.** The staged `c` handler lived in the
   PanelLeft delegate only; with focus elsewhere the key died.

Ignore semantics note: `.cvsignore` only controls what the *scanning*
commands (update/import) report. An explicit `cvs add` legitimately
overrides it — so committing an ignored-but-staged file is valid and
must work.

## Fix

- `resolveFileStatus` now applies the same CVS/Entries promotion as
  the listing: unbroken CVS/ ancestor chain + file not registered in
  its dir's Entries → `?` (not `""`).
- Staged `c` moved to the pre-delegation switch (works from either
  panel) and reports "Nothing committable …" via the banner when the
  gate is empty; the commit input's Enter/Ctrl-E do the same.
- Bonus fix while touching the add path: every `cvs add` of a file
  now passes `-kb` when the content is binary (`fs.IsBinary`) —
  without it CVS keyword-expands and newline-converts PDFs/images on
  future checkouts, silently corrupting them.

## Files touched

- `tui/app_status.go` — Entries promotion in resolveFileStatus
- `tui/app_keys.go` — focus-independent staged `c` + feedback banners
- `tui/app_actions.go` — `addArgs` helper (-kb for binaries), used by
  bulk add and single-file add
- `tui/dialog.go` — doCommit's add loop uses addArgs
- `tui/app_files_test.go` — regression tests for the promotion and -kb
