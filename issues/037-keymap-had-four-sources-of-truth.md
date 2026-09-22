# 037 — The keymap had four sources of truth

## Problem

What a key does was stated in four places that nothing kept in sync:

1. `tui/keys.go` — the bindings
2. `renderKeybar` (`tui/app_layout.go`) — the footer
3. `buildHelpContent` (`tui/dialog.go`) — the help modal
4. `README.md` / `docs/USER_GUIDE.md` — the documentation

They drifted, repeatedly. Issue 021 found five keys the UI advertised
that did nothing, one that was bound but had no handler at all, and two
shadowed handlers. Issue 024 found the same fact stated five times with
one copy wrong. Both were found by an audit, not by a test — nothing in
the build could see the difference between a key that worked and a key
the footer merely claimed.

Two specific symptoms were still live before this change:

- `p` is bound twice with identical help text (`keys.Preview` and
  `keys.SideBySide`). The preview modal it opens is advertised nowhere,
  in neither keybar nor help.
- The keybar's action row hard-coded each tab's key list, so adding a
  key meant editing the footer, the help, and the guide by hand — and
  the help's own promise ("UPPERCASE extends to the dir / every marked
  file") had long stopped matching the keys it described.

## Cause

Structural, not accidental: every surface built its own strings. A
keybar row was a literal `keyHelp(keys.Diff, keys.Commit, …)` call per
tab, a help row a literal `helpRow("d", "diff …")`. Nothing connected
the two, so "the footer says it, therefore it exists" was never
checkable — and neither was the reverse.

## Fix

`tui/keymap.go` holds one table, `keyRegistry`, with one entry per
user-facing key: its binding, semantic group, scope, the tabs it lives
in, an optional predicate, its keybar rank, and its help row. Keybar and
help modal both render from it; `keyHelp()` is gone.

The boundary that keeps the table declarative: predicates take a
`keyContext` *value* — tab, focus, cursor status, mark count, History
mode and pair state — not the `*App`. Adding app state to a predicate
means adding a field here deliberately, and the predicates are testable
without a TUI.

Bindings that are not user-facing actions are not forced into the
registry. `localBindings` holds widget-local keys (dialog y/n), and
`pendingRemoval` holds known orphans with a reason — currently
`keys.Preview`, whose modal goes away with the `d` rework.

Enforcement (`tui/keymap_test.go`):

- every `keyMap` field is in exactly one of the three lists — the check
  that would have caught issue 021's orphaned `y` binding;
- no two live entries claim the same key in the same context — the
  check that names the `p` collision;
- every keybar entry sits in a segment its tab actually renders, every
  help entry in a section the help actually prints;
- the predicates get their own table test.

The rewrite changes no user-visible text. `tui/testdata/keybar.golden`
and `help.golden` pin both surfaces across nine states (every tab, the
commit input, the three History modes, a conflict cursor, width 80) and
were written before the change; they still pass byte-for-byte after it.

## Files touched

- new: `tui/keymap.go`, `tui/keymap_test.go`
- `tui/app_layout.go` (keybar), `tui/dialog.go` (help), `tui/keys.go`
  (dead `keyHelp` removed)
- `tui/app_core.go`, `tui/app_mouse.go`: `Tab` is a named type now, so
  `tabs: []Tab{TabHistory}` in the registry is compiler-checked
