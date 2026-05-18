# 014 — Commit dialog hid input + buttons when many files were marked

## Problem

A user marked 48 files and pressed `c`. The dialog rendered with the
header `Commit (0 of 48)` and every row labeled `(unchanged)` —
producing a wall that overflowed the terminal. The message input and
the `enter:commit  esc:cancel` hint were off-screen, so the user
couldn't actually type a commit message or recover except via `esc`.

A secondary confusion: `Commit (0 of 48)` looked like a count bug.

## Cause

Two separate things:

1. **Stale marks**. `m.filelist.marked` only gets cleared by
   `commitDoneMsg` for paths that were just committed. A user can
   easily mark files, then change the underlying state outside the
   commit flow (refresh, external commit, etc.) and end up with
   marks pointing at clean files. The dialog correctly reported
   "0 committable of 48 marked" — but that label looks like a bug.

2. **No size budget**. `viewCommit` rendered every entry in `m.files`
   regardless of `m.height`. With 48 entries plus the surrounding
   chrome, the dialog box exceeded the screen — the bottom rows
   (message input, buttons) were drawn past the bottom of the
   terminal and lost.

## Fix

Two-step:

1. **Empty state** — when `committable` is 0, drop the file wall
   entirely and show "No committable files among N marked … press
   s to refresh status" with `esc:cancel`. The user sees what
   happened and recovers in one keystroke.

2. **Truncation** — when the dialog *does* have committable files,
   render only those (skipped files become a one-line summary).
   The visible count is capped to `m.height - 14` (header + skipped
   summary + message + button + box chrome). Anything above the
   cap collapses into a `… N more committable file(s)` line so the
   message input and buttons always stay visible.

Not fixed: actual scrollable pagination inside the dialog. The
truncated view is enough for the typical commit-many-files case;
a `viewport.Model`-backed scroll could be added later if a user
needs to inspect a specific entry past the cap.
