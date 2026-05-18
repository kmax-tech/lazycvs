# 015 — Subdir status display missed `.cvsignore` matches and Entries gaps

## Problem

Two related listing bugs surfaced one after the other:

1. **Ignored subdirs stayed visible.** With `hideIgnored` on, the
   right pane filtered ignored files but not ignored directories.
   Build dirs (`build/`, `dist/`, `node_modules/`) covered by a
   `.cvsignore` pattern still rendered as normal entries and
   cluttered the listing.

2. **Files in a just-added empty dir had no status badge.** After
   `cvs add fallacies-material` the directory got a `CVS/` marker
   but its `Entries` file stayed empty until the files inside were
   added too. The 48 PDFs in that dir rendered with `*` (marked)
   but no letter — they looked "up-to-date" when in reality they
   were untracked.

## Cause

1. The right-pane row builder filtered files via `showFile()` (which
   honors `f.Ignored`) but had no equivalent for subdir headers.
   `SubDirGroup` didn't even carry an `Ignored` flag, so there was
   nothing to filter on.

2. `resolveFileStatus` walked the path's ancestor chain looking for
   any directory missing `CVS/`. If every ancestor had `CVS/`, it
   returned `""` (clean). It never consulted `CVS/Entries` of the
   immediate parent — so a freshly-`cvs add`-ed dir, with `CVS/`
   present but `Entries` empty, made every file inside look tracked.

## Fix

1. Added `Ignored bool` to `SubDirGroup`. Set in
   `updateFileListForDir` when the dir matches the parent's pattern
   set *and* isn't itself CVS-tracked (a tracked dir is never
   hidden, so a user-edited file inside a tracked dir whose name
   happens to match `build` stays reachable). `rows()` skips
   ignored dir headers under `hideIgnored`; when shown, the header
   uses the same dimmed strikethrough style as ignored files.

2. Added `readCVSEntries(absDir)` — reads `<absDir>/CVS/Entries`
   once per dir listing, returns a name → bool set of tracked
   entries (both files via `/name/…` lines and dirs via
   `D/name/…`). In `updateFileListForDir`, when a file's
   `resolveFileStatus` came back `""`, the file's basename is
   looked up in the parent's Entries set; absence promotes the
   status to `"?"`. Same treatment for files inside each visible
   subdir (its own Entries is read once and used for all its
   files). Falls back to the previous behavior when Entries is
   missing or unreadable.

The combined effect: a fresh `cvs add <dir>` leaves the dir
itself as `A`, its files render as `?` so the user knows what
still needs `a`, and any pattern in the dir's `.cvsignore` is
applied to those `?` entries (matching cvs(1)'s precedence).
