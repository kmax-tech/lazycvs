# 044 — "Staged" promised an index CVS does not have

## Problem

Tab 3 was called *Staged*. CVS has no index: nothing is staged, and
nothing is committed from a staging area. What the tab shows is the set
of files the user marked with `space` — a worklist that `c`, `r`, `i`
and `u` read when deciding what to act on.

The same set went by three names: the tab said *Staged*, the key said
*mark*, the help called it a *worklist*, and the code called the map
`marked`. A user reading the tab title learned a model the rest of the
app does not use.

Worse, opening the tab **changed** the set: with nothing marked yet, it
marked the file under the cursor. Navigation silently edited a worklist
that the next bulk action then acted on.

## Fix

One word everywhere the user can see: **Marked**. The tab is
`3:Marked(2)`, the panel title is `Marked (n)`, `space` in the tab says
`unmark` rather than `unstage`, and the refusal messages speak of marked
files. The empty state now explains the mechanism rather than naming it:

```
Nothing marked

Press space on a file in the
Files tab to mark it. Marked
files are what c, r, i and u
act on.
```

Opening the tab no longer marks anything.

Internal identifiers (`StagedModel`, `TabStaged`, `staged.go`) keep
their names for now: renaming types is a mechanical change that would
bury this one in noise, and it is a separate commit.

One thing found while renaming: the tab labels were built twice — once
by the renderer and once by the mouse handler, which measures them to
find which tab a click hit. Renaming a tab in one place would have
shifted every click target in the other. `tabNames`/`tabLabel` are now
the one source, with a test that clicking the middle of each label
selects that tab.

## Files touched

- `tui/app_layout.go` (`tabNames`, `tabLabel`, panel title),
  `tui/app_mouse.go` (reads them)
- `tui/staging.go` (header, empty state), `tui/app_keys.go` (no
  auto-marking, refusal wording), `tui/keys.go`, `tui/keymap.go`
- `tui/app_keys_test.go`, goldens
