# 005 — Stale diffs on fast scroll + keybar disappearing

## Problem

Two issues in the History tab:

1. **Stale diff flashes**: Scrolling quickly through revisions fired a CVS diff command per cursor position (~260ms each). Results arrived out of order, briefly displaying the wrong diff for the current cursor before the correct one loaded. Issue 003's `ready = false` fix showed "Loading..." instead of the *previous* stale content, but late-arriving results from intermediate positions still overwrote the viewport with wrong content.

2. **Keybar disappearing**: When the console panel was enlarged (via `+` key) on a small terminal, `contentHeight` was clamped to a minimum of 5 without reducing `consoleHeight`, so the total rendered height exceeded `m.height`. The `View()` trimming code tried to preserve the keybar as the last line, but `lipgloss.JoinVertical` could introduce subtle misalignment making `lines[len(lines)-1]` not the keybar.

## Fix

1. **Generation counter**: Added a `loadGen` counter to `HistoryModel`. Each `loadHistoryContent()` call increments it and embeds the current gen in the dispatched async message (`historyContentMsg`, `historyDiffMsg`, `historyCompareMsg`). When a result arrives, the handler checks `msg.gen != m.loadGen` and silently drops stale results. Fast scrolling now shows "Loading..." until the final cursor position's result arrives — no flashing of intermediate diffs.

2. **Layout fix**: `layout()` now reduces `consoleHeight` when `contentHeight` hits minimum 5, ensuring `contentHeight + consoleHeight + overhead == m.height` invariant holds. `View()` was also rewritten to explicitly reserve one line for the keybar instead of relying on a fragile trim-and-replace of the "last line."

3. **Minor**: History tab's left-panel info line now uses `numRows()` instead of `len(revisions)` to correctly count the working-copy pseudo-row.
