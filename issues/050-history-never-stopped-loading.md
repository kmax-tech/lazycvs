# 050 — The History tab loaded forever when there was nothing to load

## Problem

Opening the History tab on a file CVS does not track — an untracked
`?` file, or one added but never committed — showed:

```
  Loading revisions...          Loading diff...
```

and kept showing it. Not for a moment: permanently. `cvs log` had
already come back with nothing; there was simply no way to say so.

## Cause

`HistoryModel.ViewLeft` read "zero rows" as "still loading":

```go
rows := m.numRows()
if rows == 0 {
    return mutedStyle.Render("  Loading revisions...")
}
```

The model had no state for "the log arrived and it was empty", so
pending and empty rendered identically — and pending is the one that
never resolves.

## Fix

`loaded` and `emptyNote` on the model, set by `MarkLoaded` when
`historyLoadedMsg` lands and reset by `SwitchTo`. Empty now explains
itself, and the three empty cases get three sentences, because they
have three different next steps:

- `?` — *Not in CVS — no revisions yet. Press a in the Files tab to add it.*
- `A` — *Added but not committed. The first commit creates revision 1.1.*
- otherwise — *No revisions returned. The Console has what cvs said.*

The App phrases the note (it owns the status map) and the model renders
it, keeping the data/rendering split the architecture asks for. The
text wraps to the revision pane's width — an explanation that runs off
the edge of a 38-column pane explains nothing — and the right pane
stays empty instead of claiming to load a diff of nothing.

## Files touched

- `tui/history.go` — `loaded`/`emptyNote`, `MarkLoaded`, both panes'
  empty states
- `tui/app_history.go` — `historyEmptyNote`
- `tui/app_core.go` — `MarkLoaded` on `historyLoadedMsg`
- `tui/header_test.go`
