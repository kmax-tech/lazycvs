# Ignored files indistinguishable from tracked clean files

## Problem

Files with no CVS status marker (empty string) could be either:
1. Tracked and up-to-date (committed, no local changes)
2. Untracked but ignored by CVS (via `.cvsignore` or global ignore patterns)

Both cases rendered identically in the file list — no status marker, normal text.
Users had no way to tell whether a file like `.blg` was committed or just ignored.

## Cause

`cvs update -n -q` reports untracked files as `?`, but ignored files are silently
skipped. The `statusMap` has no entry for either tracked-clean or ignored files,
so the TUI treated them the same.

## Fix (attempt 1 — CVS/Entries, failed)

Tried parsing `CVS/Entries` to build a set of tracked filenames. Idea: if a file
has no status AND is not in `CVS/Entries`, it must be ignored. This didn't work
reliably because:

- **Entries vs Entries.Log**: CVS writes pending changes to `CVS/Entries.Log`
  which is merged lazily into `CVS/Entries`. Reading only `Entries` misses files
  that haven't been merged yet.
- **Negative logic is fragile**: "not in Entries" doesn't distinguish between
  "ignored" and "new file not yet added". The distinction requires knowing the
  actual ignore patterns.

## Fix (attempt 2 — .cvsignore pattern matching, works)

**Pattern loading** (`loadIgnorePatterns`, `readIgnoreFile` in `app_helpers.go`):
Reads `~/.cvsignore` (global) + `.cvsignore` in the directory. Collects all
glob patterns, skipping comments (`#`) and empty lines.

**Pattern matching** (`matchesIgnore` in `app_helpers.go`):
Uses `filepath.Match` (fnmatch-style globs) to test each filename against the
collected patterns. This mirrors CVS's own ignore logic.

**Ignored detection** (`updateFileListForDir` in `app_helpers.go`):
A file is marked `Ignored: true` when it has empty status AND its basename
matches an ignore pattern. Applied to both top-level files and files inside
subdirectory groups.

**Visual rendering** (`filelist.go` View, `styles.go`):
Ignored files use `ignoredStyle` — strikethrough + dark gray (256-color 239) —
clearly distinguishable from normal text on any terminal theme. Earlier attempt
with `mutedStyle` (ANSI color 8) was too subtle to see. Result:
- No marker, normal text → tracked + clean
- Strikethrough, dark gray → ignored
- `?` → untracked, not ignored
- `M`/`C`/`A`/etc. → tracked with changes

**FileEntry** (`cvs/types.go`):
Added `Ignored bool` field to `FileEntry` to carry the flag from detection to
rendering without introducing a fake CVS status code.
