# 049 — Nothing on screen said which branch you were on

## Problem

The tab bar's right-hand side was empty apart from the error counter.
Three questions had no answer anywhere in the UI:

1. **Which branch is this?** CVS records stickiness **per directory**.
   `cvs update -r release-2` inside one subdirectory pins that
   subdirectory alone; every command run there afterwards works against
   that branch while the rest of the tree stays on the trunk. The file
   listing looks identical either way. An afternoon's work can land on
   a branch nobody meant to be on, and the first sign of it is a commit
   in the wrong place.
2. **Which checkout is this?** With several working copies of the same
   module open, only the console's title said which one — and only
   because issue 024 put the working-copy name there.
3. **How old is what I see?** The status of every file is a claim about
   the server, made at some point in the past. Nothing said when.

## Fix

The tab bar's free space now carries all three:

```
 1:Files │ 2:Fav │ 3:Marked │ 4:History        demo · release-2 · ⟳ 3m
```

- **Module** from `CVS/Repository`, read once — it cannot change under
  a running checkout.
- **Sticky tag** of the directory the *file pane is showing*, not of the
  checkout root, because that is where CVS applies it. Trunk reads
  `MAIN` in grey; a sticky tag is yellow; a non-branch tag adds
  `no-commit`, since committing to one fails and reading that here is
  cheaper than reading cvs's refusal afterwards.
- **Age** since the last whole-tree check (startup's recursive scan,
  `s`, or a post-action refresh). Per-directory scans deliberately do
  not reset it: they happen on every navigation, and an age that always
  reads "now" answers nothing. Before the first check the slot is
  empty rather than "0m", which would claim a check that never ran.

A 30-second tick (`clockTickMsg`) exists only so the age advances while
the user does nothing. A header that says "2m" twenty minutes later is
worse than one that says nothing.

When the width runs out the parts go in order of what their absence
costs: the age first, the module second, the branch never.

`cvs.ReadSticky` parses `CVS/Tag` (`T` branch, `N` non-branch tag, `D`
date) and treats a missing or unreadable file as the trunk — the same
conclusion CVS draws, so there is no error for the UI to explain.

## Files touched

- new `cvs/sticky.go`, `cvs/sticky_test.go`
- `tui/app_layout.go` — `headerContext`, `refreshAge`, tab-bar wiring
- `tui/app_core.go` — `module`/`sticky`/`statusAt`, the clock tick
- `tui/app_files.go` — sticky follows the shown directory
- `tui/external.go` — `moduleName`, the display-side wrapper
- new `tui/header_test.go`, `README.md`, `docs/USER_GUIDE.md`
