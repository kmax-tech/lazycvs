# 011 — Latin-1 file content causes render glitches across panels

## Problem

After the History refactor (issue 010), users with files in Latin-1
encoding still saw the History panel rendering corruption: the right
pane showed `untersuch��`, `f�r`, `Them` on its own line, etc.; the
left pane appeared to have duplicate rows ("1.13" twice with one row
missing its description); a terminal resize temporarily fixed it but
scrolling re-introduced the artifacts.

The user confirmed the artifacts appeared in both VS Code and Kitty,
ruling out a terminal issue, then noted the affected files were stored
in **Latin-1 (ISO-8859-1)** rather than UTF-8.

## Cause

CVS is encoding-agnostic and emits whatever bytes the source file
contains. A Latin-1 byte like `0xFC` ("ü") is **not** a valid UTF-8
sequence on its own. When that byte hit the rendering pipeline:

1. `lipgloss` / `x/ansi` `StringWidth` computed the wrong cell count
   for the line, because invalid UTF-8 bytes get tracked as zero-width
   replacement characters.
2. Wrong width counts meant `renderPanel` either over- or under-padded
   the line and skipped the truncation it would otherwise apply.
3. The resulting line was wider than the panel's inner width, so the
   terminal wrapped it.
4. The terminal-level wrap shifted everything below it; in
   `JoinHorizontal` the right pane's overflow desynced the row pairing
   with the left pane, which is why the left pane's revisions list
   appeared shifted/duplicated.
5. Replacement characters (`�`, U+FFFD) appeared wherever the
   terminal decoded the lone Latin-1 high bytes.

A resize masked the symptom temporarily because the new layout was
recomputed and then the next scroll re-fed the bad bytes into the
pipeline.

## Fix

Added `cvs.EnsureUTF8(s string) string` (`cvs/encoding.go`):

```go
func EnsureUTF8(s string) string {
    if utf8.ValidString(s) {
        return s
    }
    runes := make([]rune, len(s))
    for i, b := range []byte(s) {
        runes[i] = rune(b)
    }
    return string(runes)
}
```

Latin-1 maps 1:1 onto Unicode code points U+0000–U+00FF, so casting
each byte to a `rune` and re-encoding via `string([]rune)` produces
correct UTF-8 with no information loss.

`EnsureUTF8` is applied to every cvs output that flows into a renderer:

- `loadRevisionContent` (cat single revision)
- `loadRevisionDiff` (parsed before `cvs.ParseDiff`)
- `loadBlame` (annotate output)
- `loadPreviewDiff` (TreeViewDetails preview)
- File-preview overlay opened from the tree (`os.ReadFile` of working copy)

UTF-8 input is fast-pathed through `utf8.ValidString`; only invalid
bytes pay the conversion cost.

## Why this surfaces in the History panel specifically

The status / log / annotate parsers don't display raw byte content the
same way — their tokens are mostly ASCII (statuses, dates, revision
numbers, author names). The diff and revision-content paths are the
ones that pipe arbitrary file content straight to the renderer, which
is why the artifacts were concentrated in the History tab's right pane
and only appeared on Latin-1 files.
