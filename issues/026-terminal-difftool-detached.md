# 026 — Terminal diff/merge tools were launched detached and fought the TUI

## Problem

The `vimdiff` preset for `[editor] diff_tool` / `merge_tool` (E and M keys)
was unusable: launching it garbled both the diff tool and the lazycvs TUI,
which kept painting over each other.

## Cause

`launchExternalDiff` / `launchExternalMerge` started every tool with
`cmd.Start()` — fire-and-forget, detached. That is correct for GUI tools
(meld, vscode, opendiff), but a curses program like vimdiff needs exclusive
control of the terminal, which the still-running Bubble Tea program also
holds. Both wrote to the same tty concurrently.

## Fix

`spawnTool` (tui/external.go) now checks `isTerminalTool` (argv[0] basename
in vim/vimdiff/nvim) and routes terminal tools through `tea.ExecProcess`,
which suspends the TUI, hands the terminal to the tool, and restores the TUI
on exit — the same mechanism `e` (edit in $EDITOR) already used. GUI tools
keep the detached path. Also added an `nvimdiff` preset (`nvim -d`) for both
diff and merge.
