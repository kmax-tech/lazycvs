# 054 — Every diff ended with a phantom line, and annotations were numbered

## Problem

Two defects in how a unified diff was read and drawn:

1. **A numbered blank row at the end of every diff.** `ParseDiff` split
   the output on `\n`; a diff ends with one, so the split left a
   trailing empty element that became a context line — with a line
   number, which also meant the counters ran one past the file.
2. **`\ No newline at end of file` was rendered as a line of the
   file**, with a number of its own. It is diff's annotation about the
   line above it. Numbering it pushed every number after it one too
   far, in exactly the situation where a reader is counting lines.

```
    1 - letzte Zeile ohne Umbruch
    1   \ No newline at end of file      ← not a line, and not line 1
    2 + geaenderte letzte Zeile           ← this is line 1
    3   \ No newline at end of file
    4                                     ← does not exist
```

## Fix

- The trailing empty element is dropped before parsing. A genuinely
  empty context line inside a hunk survives, because unified diff
  writes one as a single space, which is a different string.
- `DiffLineNote` is its own line type: no numbers, counters untouched,
  and the renderer prints it dimmed in the number column's empty space.

While in the renderer, `renderSBSLine` was still cutting content with
`content[:width-7]` — bytes again, so an umlaut in a side-by-side diff
could be halved, and a pane narrower than seven columns would panic on
a negative index. It uses `padCells` now, like every other column.

The unified renderer also switched from string literals (`"added"`) to
the `cvs.DiffLine*` constants it was informally duplicating.

## Files touched

- `cvs/diff.go`, `cvs/diff_test.go`, `tui/diff.go`
