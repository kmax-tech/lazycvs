# 045 — Four defects the test suite could not see

## How they were found

The plan's manual checklist ("open the demo working copy, walk the
cursor, press `d`, watch the keybar") was run against the real binary in
a pseudo terminal, with the escape stream replayed onto a character grid
so each frame could be read the way a user sees it. Every defect below
passed the unit tests and failed on screen.

## 1. The collapsed console said `cvs status status  8.933709ms`

The one-line console view (issue 042) built its own line from
`e.Command` plus `e.Args` — but `Command` already carries the full
command line, so the subcommand appeared twice, and the duration went
unrounded while the expanded view shows `(9ms)`.

Two renderings of one thing, drifting immediately — the pattern issue
024 catalogued. `compactLine(e)` is now the single formatter; the
compact filter and the collapsed panel both call it.

## 2. The diff pane reported "No local changes" for a modified file

`d` on `docs/notes-de.txt` (status `M`) opened the diff layout and said
the file was unchanged. The keybar dropped `c` and `r` on the same row.

`ExpandToPath` loads a directory's children **synchronously** while
revealing a path, so rows can enter the tree that no status pass has
ever annotated. Their `Status` is `""`, and everything that asks a node
for its status — `autoLoadPreview`, the keybar's `keyContext` — read
that as "clean". Files only appear in the tree in the diff layout,
which is why `d` made it visible.

`revealPath` now annotates after expanding (all three callers benefit:
the fuzzy finder, the command-line path, the diff toggle), and
`autoLoadPreview` asks `resolveFileStatus` instead of the node it is
about to draw — `App.statusMap` is the truth, a node's `Status` is a
copy from the last annotation.

## 3. Focusing the console opened it empty

With the console collapsed to one line, its viewport's scroll offset
sits at the last line of the content. Expanding to six lines without
re-anchoring scrolls past the end, so the panel rendered blank — right
when the user pressed `Ctrl-J` to look at something. `SetSize` now
re-anchors on a height change.

## 4. The quit warning pointed at the console

`q` with marks warns on the banner, and the banner appended
`(C-j: console, errors only)` to anything red. A refusal is not a failed
command: the console had nothing to add. The hint (and `Ctrl-J`'s jump
to the errors filter) now require an **unread error in the command log**
as well as a red banner — `bannerPointsToConsole`.

## What the checklist confirmed

- keybar per status: `?` → `a`/`i`, `M` → `c`/`d`/`r`, `A` → neither
  `a` nor `r`, `C` → `M:merge tool` (which survives the width squeeze
  while `H:recent` is dropped, as the segment ranking intends)
- `1 marked ▸` leads the row, `x:clear marks` appears with it
- widths 60/80/120: rows end in `+6 (?)`, never `…`
- `d` opens the diff on the same file and closes back onto the same row
- header and footer agree (`14 files` / `4 of 7`)
- German diffs render intact (`Schöne neue Überschrift hinzugefügt`)

## Files touched

- `tui/console.go` (`compactLine`, `SetSize` re-anchor)
- `tui/app_core.go` (`revealPath` annotation, `bannerPointsToConsole`)
- `tui/app_history.go` (`autoLoadPreview` asks the App)
- `tui/app_keys.go`, `tui/app_layout.go` (banner hint rule)
- tests: `TestRevealAnnotatesRowsItJustExpanded`,
  `TestConsoleShowsContentAfterExpanding`, and the console-jump test
  now covers the quiet-refusal case
