# 021 — Keybinding audit: dead keys, shadowed handlers, stolen digits

## Problem

A cross-surface audit (keyMap → handlers → keybar → help → user guide)
found several keys that the UI advertised but that did nothing, one
input bug, and dead code shadowed by earlier handlers in the dispatch
chain:

1. Typing `1`–`4` into the Staged commit input switched tabs instead of
   inserting the digit — "fix issue 42" threw the user onto another tab
   mid-sentence.
2. `u` (update) on the Staged tab did nothing, although the keybar and
   the guide advertised a bulk update of the staged set.
3. `+` / `-` (add/remove favorite) had no handler at all — the
   Favorites empty state even says "Press + to add".
4. `y` (copy console entry) had no handler but was listed in help.
5. `c` on the Staged tab opened the modal commit dialog instead of the
   in-pane commit input the guide describes; the in-pane path was
   unreachable via `c`.
6. `f`/`I` in `filelist.go` were dead copies of the App-level handlers
   — except on the Favorites tab, where the stale `I` copy was live and
   toggled hide-ignored on the list only, out of sync with the tree.
7. `x` in the Staged tab was an undocumented duplicate of `space`
   (unstage), conflicting with `x` = clear in the console.

## Cause

The key dispatch chain is staged-input → overlays → global → panel
delegate. Handlers added at an early stage silently shadow later ones:
the global `u` consumer predated the Staged bulk-update handler, the
global tab-digit bindings predated the staged commit input, and the
file-action `c` case predated the in-pane commit mode (the sibling `r`
case had already been gated with `!= TabStaged` for exactly this
reason). `+`/`-`/`y` were bound in the keyMap and advertised in the
help/keybar but never given handlers.

## Fix

- Removed the Tab1–4 passthrough in `handleStagedInput` — digits now
  type into the commit message; tab switching while composing goes
  through Esc first (same rule as dialogs and the search overlay).
- `handleGlobalKey` lets `u` fall through on the Staged tab so the
  bulk-update handler receives it; History still consumes it silently.
- Gated the file-action `c` with `!= TabStaged`, mirroring the `r` gate.
- Implemented `+`/`-`: `App.addFavorite` / `App.removeFavorite` persist
  through `ConfigManager.Update` and mirror into the FavoritesModel.
- Deleted the dead `y` (Copy) binding and its help line.
- Deleted the shadowed `f`/`I` cases in `filelist.go`; the global `I`
  handler now also covers the Favorites tab so both panes stay in sync.
- Dropped the undocumented staged `x` alias; `space` is the unstage key.

## Files touched

- `tui/app_keys.go`, `tui/keys.go`, `tui/filelist.go`,
  `tui/favorites.go`, `tui/app_actions.go`, `tui/app_layout.go`,
  `tui/dialog.go`
