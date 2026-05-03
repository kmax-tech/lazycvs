# 006 — `d` key in history tab reloads entire history instead of toggling mode

## Problem

Pressing `d` while in the History tab reloaded the full CVS log and reset the
cursor to position 0 instead of switching the right panel to diff mode. This
caused:

- **Lost scroll position**: cursor jumped back to the first revision.
- **Duplicate CVS commands**: the reload triggered a fresh `cvs log` plus a
  redundant `loadHistoryContent()` for position 0, producing duplicate diff
  commands visible in the Console panel.
- **Broken mode toggle**: users could not switch from content/blame/compare
  back to diff mode with `d`, because the key was intercepted before
  `HistoryModel.Update()` could handle it.

## Cause

In `delegateKey`, the file-action block matched `keys.Diff` regardless of the
active tab:

```go
case key.Matches(msg, keys.Diff) && selectedPath != "" && !fs.IsBinary(...):
    m.activeTab = TabHistory
    return loadHistory(...)
```

When `activeTab` was already `TabHistory`, `selectedPath` resolved to
`m.history.path` (non-empty), so the condition matched. The handler fired
`loadHistory()` for the same file, reloading the log and resetting all state.
The downstream `historyLoadedMsg` handler then dispatched `loadHistoryContent()`
for cursor 0, duplicating earlier diff commands.

Because the file-action block returned before reaching the navigation delegate,
`HistoryModel.Update` never saw the `d` key and never toggled the mode.

## Fix

Added `m.activeTab != TabHistory` to the `keys.Diff` file-action condition so
that `d` falls through to the navigation delegate when already in the history
tab. `HistoryModel.Update` then handles it correctly: sets `mode = HistoryDiff`,
clears compare state, and lets the existing cursor-change detection in
`delegateKey` fire `loadHistoryContent()` only when the mode actually changed.

Also extended the cursor highlight in `ViewLeft()` to cover both `line1` and
`line2` of the selected revision row (previously only `line1` was reversed),
making the selection boundary unambiguous.
