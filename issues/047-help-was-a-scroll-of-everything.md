# 047 — The help answered every question except the one being asked

## Problem

`?` opened a hundred-plus lines of keymap, always at the top, always
"Navigation" first, with no way to search. Pressing `?` in the History
tab — which means "what can I do *here*" — meant scrolling past
Navigation, Tabs, File actions and View & filters to reach the answer.

Three smaller faults made it worse:

- The viewport was sized to `min(width-6, 78)` while the dialog frame it
  lives in is capped at 55 and carries `Padding(1, 2)`, which lipgloss
  counts *inside* that width. The text area is 51 columns; the content
  was built for up to 78. Long rows were wrapped twice — once by us,
  once by the viewport, the second time back to column 0, where the
  continuation collided with the key column and the list read as prose.
- Nothing said the help could be filtered, because it could not be.
- The section legend for status codes sat at the very bottom, after
  everything.

## Fix

**Opens where you are.** `helpSectionFor(keyContext)` maps the current
situation to a section — console focused → Console, History tab →
History tab, otherwise File actions — and `OpenHelp` scrolls there.
The whole list is still present above and below; only the starting
point moved.

**`/` filters.** Typing narrows the rows to those whose key text or
description contains what you typed, drops sections that keep nothing,
and hides the prose between sections — when you are hunting one key,
paragraphs are in the way. `Enter` keeps the result and returns to
scrolling; `Esc` clears the filter; `Esc` on an unfiltered list closes
the dialog, the same "one layer at a time" rule the History tab uses
for its comparison. While typing, the keys belong to the filter — `q`
is a letter there, not quit.

**One width.** `dialogBoxWidth` is now the single source for the frame,
and the help viewport derives from it (minus the four columns of
padding). Descriptions wrap to that width with a hanging indent at the
key column, so a wrapped row stays a row.

The unfiltered text is unchanged apart from the wrapping — the golden
diff is all indentation.

## Files touched

- `tui/dialog.go` — `OpenHelp(section)`, `helpSectionFor`,
  `setHelpContent`, `scrollHelpTo`, filter keys, `buildHelpBody(filter,
  width)`, `helpRow` wrapping, `dialogBoxWidth`, footer
- `tui/app_keys.go` — `?` passes the context's section
- `tui/keymap_test.go`, `docs/USER_GUIDE.md`, `README.md`, goldens
