# 016 — `.cvsignore` rendered as a phantom `(server)` entry

## Problem

A user with a per-directory `.cvsignore` checked into CVS saw it
listed in the right pane with the `(server)` marker (meaning "tracked
on the server, not on disk yet") even though the file was sitting
right there in the same directory. Marking it for commit wasn't
possible because the entry behaved like a server-only stub.

## Cause

Two independently-correct assumptions collided:

1. **`skipInListing` filter (tui/app_files.go:88, old version)** — for
   the right-pane `os.ReadDir` walk we hid "meta" entries:

   ```go
   func skipInListing(name string) bool {
       return name == "CVS" || name == ".cvsignore" ||
              name == ".DS_Store" || ...
   }
   ```

   The idea was to treat `.cvsignore` like `CVS/` — internal plumbing
   the user shouldn't have to scroll past.

2. **ServerOnly fallback (tui/app_files.go:208 ff.)** — paths in
   `m.statusMap` that the `os.ReadDir` walk hadn't seen got
   reintroduced as `ServerOnly: true` entries so a `?` / `U` reported
   by cvs but missing from the filesystem still appears in the
   listing:

   ```go
   for path, status := range m.statusMap {
       if seen[path] { continue }
       files = append(files, cvs.FileEntry{..., ServerOnly: true})
   }
   ```

   The implicit invariant was `seen[path] == "path exists on disk"`.

`.cvsignore` was the only entry in the skip list that **could also be
CVS-tracked**:

- `CVS/` is a directory and isn't reported as a file by cvs status.
- `.DS_Store` is never tracked.
- `.#*` lock files are never tracked.

So when `cvs status` reported the tracked `.cvsignore`, it landed in
`statusMap`. `os.ReadDir` *did* see it locally — but `skipInListing`
removed it before `seen` got populated. `seen[".cvsignore"]` was
therefore `false`, the ServerOnly fallback kicked in, and a phantom
"(server)" entry appeared for a file that was actually right there.

The broken invariant was subtle: `seen` had silently shifted from "is
on disk" to "is on disk AND not hidden by us".

## Fix

Removed `.cvsignore` from `skipInListing`. The local file now flows
through `os.ReadDir` like any other entry, lands in `seen`, and the
ServerOnly fallback no longer misclassifies it.

Side-benefit: users can now see, mark, edit, and commit
`.cvsignore` directly from the file list — which is what CVS expects
for a per-directory ignore file (it's intentionally team-shared via
commits). Personal-only patterns still belong in `~/.cvsignore`
(global) via lazycvs's `i` dialog options [2]/[3], which never touch
a CVS repository.

## Notes

If the same `seen`-as-presence assumption is added elsewhere later,
the rule is: anything that exists on disk has to land in `seen`,
even when the UI chooses to hide it from the listing. Equivalently,
the skip filter should run *after* `seen` is built, not before.
