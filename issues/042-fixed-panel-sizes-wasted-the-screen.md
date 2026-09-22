# 042 — Fixed panel sizes wasted a quarter of the screen

## Problem

Two sizes were hard-coded and neither adapted to anything:

1. **The console was always six lines tall** (`consoleHeight: 6` in
   `NewApp`), plus a border and padding — about a quarter of a
   25-line terminal, permanently, to display one line of content most
   of the time. The panel is a log: it matters after a failure and
   while you are reading it, and is decoration the rest of the time.
2. **The tree was pinned between 25 and 40 columns** whatever the
   terminal's width. On a 200-column window the tree got 40 and the
   file list 160 — the one pane where long paths get truncated was the
   one kept narrow. Neither size was configurable: `[ui] tree_width`
   appeared in the design spec, was parsed by nothing, and read by
   nothing.

## Fix

A `[ui]` config section with `tree_width` and `console_height`, resolved
once at startup into `App.ui` so the render path never touches the
config manager.

The console's height becomes a pure function of a small state value:

```go
func consoleHeight(ui uiSizes, st consoleState) int
```

- neither focused nor holding an unread error → one line, showing the
  command that just ran (`collapsedView`, because a one-line viewport
  scrolled to the bottom shows the *status* line with the command out
  of sight above it)
- focused, or an error the user has not seen → the configured height
- `+` / `-` while focused set that expanded height for the session

Two deviations from the plan:

- The plan tracked an `errorSeen` flag. It is not needed: focusing the
  console already clears the unread-error counter (`App.Update`), so
  "until the user has looked" falls out of the counter that exists.
- The plan let `+`/`-` expire at the next tab switch. Expiring a size
  the user deliberately dialled in is surprising; it now lasts the
  session, which is what the old `consoleHeight` variable did.

`treeWidth` keeps the automatic third-of-the-terminal rule when
unconfigured, and never lets a configured width take more than half the
screen or fall below 15 columns.

Focus changes now trigger `updateSizes` (`resizeOnFocusChange`), since
the panels' heights depend on focus: the key and mouse paths capture
the focus before dispatch and re-lay-out when it moved. Extracting
`handleKey` from `Update` was needed to have one place to compare.

Tests (`tui/layout_test.go`): the transition table, the
failure → read → collapse sequence, the width clamps, and that nothing
but the three declared inputs can change the height — a panel that
resizes while you read it is worse than one that is too small.

## Files touched

- `config/types.go`, `config/config.go` — the `[ui]` section
- `tui/app_layout.go` — `uiSizes`, `consoleState`, `consoleHeight`,
  `treeWidth`
- `tui/app_core.go` — `ui`/`consoleOverride` fields, `consoleH`,
  `handleKey`, `resizeOnFocusChange`
- `tui/app_keys.go` — `+`/`-` set the expanded height
- `tui/console.go` — `collapsedView`
- new `tui/layout_test.go`

Documentation of the new keys follows with the rest of the docs pass.
