# 011 — Latin-1 file content causes render glitches across panels

## Problem

After the History refactor (issue 010), the user reported intermittent
visual corruption in the History tab:

- **Right pane** showed `untersuch��`, `f�r`, `Them` on its own line —
  text broken at apparently random positions, with U+FFFD replacement
  characters appearing where umlauts should have been.
- **Left pane** appeared to have duplicate rows — e.g. "1.13" listed
  twice, with one occurrence missing its description line and the rows
  below shifted by one.
- **Resizing the terminal** temporarily fixed the layout, but
  scrolling immediately re-introduced the artifacts.
- **Both VS Code and Kitty** terminals exhibited the issue, so it was
  not terminal-specific.

## Investigation: three wrong hypotheses

This took several iterations to diagnose. Each wrong hypothesis is
worth recording because each one *looked* correct from the symptoms
alone, and the disproof of each one narrowed the search.

### Hypothesis 1: Stale-cell artifact from differential rendering

Bubbletea uses a differential renderer: it tracks the previously
written terminal state and only emits writes for cells whose content
changed. A common failure mode is "two cursor highlights at once" —
the new cursor row is highlighted, but the old cursor row also still
appears highlighted because Bubbletea decided the cells didn't change.

**Fix attempted (commit 803b590):** Add a column-0 marker (`▌` for
cursor row, ` ` for others) so each row's first byte definitely
changes when the cursor moves. Forces redraws.

**Why it was wrong:** The duplicate-row artifact persisted. Adding
the marker did make cursor selection visually clearer, but didn't
solve the actual corruption.

### Hypothesis 2: Initial-frame painted onto a non-blank buffer

The user reported "resize fixes it temporarily," which fits a classic
pattern: `tea.WithAltScreen` enters the terminal's alt-screen buffer
but doesn't always clear it before the first paint. Cells outside
that first frame's bounds hold whatever was on screen before launch.

**Fix attempted (commits 7d58a2f and 617131e):** Issue
`tea.ClearScreen` from both `Init()` and the `WindowSizeMsg` handler,
guaranteeing every paint starts from a known-empty buffer. Also
re-render right-pane content on width change so cached `rawView`
doesn't get stretched/squeezed for a different width.

**Why it was wrong:** The artifacts persisted. The user noted: "a
resizing forces the correct view but then *scrolling* creates the
glitches." That observation was the disproof — if it were stale
cells, scrolling alone shouldn't matter; new content should overwrite
old content. The corruption was being **produced fresh** on each
scroll, not inherited from a previous frame.

### Hypothesis 3: Viewport soft-wrap on long lines

I checked `bubbles/viewport` v1.0.0 source. Its `View()` calls
`lipgloss.NewStyle().Width(w).MaxWidth(w).Render(content)`. `MaxWidth`
truncates with `ansi.Truncate`, which is grapheme-aware and UTF-8
safe. So the viewport wasn't soft-wrapping at the wrong place.

This was the right code path to look at, but the wrong layer.

## Actual cause

The user provided the missing piece: **the source files in this CVS
repo are stored in Latin-1 (ISO-8859-1)**, not UTF-8.

CVS is encoding-agnostic — it stores and emits raw bytes from
whatever encoding the source files use. A Latin-1 byte like `0xFC`
("ü") on its own is **not** a valid UTF-8 sequence. The whole
rendering stack assumes UTF-8:

1. Diff/content output reaches the renderer with invalid UTF-8 bytes
   embedded.
2. `lipgloss` / `x/ansi`'s `StringWidth` walks the string treating
   invalid byte sequences as zero-width replacement characters, so it
   miscounts the line's actual cell width.
3. `renderPanel` uses that miscounted width to decide how much to
   pad/truncate. Both decisions come out wrong.
4. The line emitted into the terminal is **wider than the panel's
   inner width**.
5. The terminal hits the right margin and **wraps** the line. That
   wrap pushes a row of right-pane content onto the next visual line.
6. `lipgloss.JoinHorizontal` had paired left-pane row N with
   right-pane row N. When the terminal-level wrap inserts an extra
   visual line on the right side after rendering, every row below it
   appears one line lower than the corresponding left-pane row — which
   is exactly the "duplicate 1.13" / "missing description" / "shifted
   rows" pattern.
7. The U+FFFD `�` characters appear wherever the terminal had to
   decode a lone high byte that wasn't valid UTF-8.

This explains every observed symptom from one root cause:
- `untersuch��` → terminal wrapped the line + decoded a Latin-1 byte
- duplicate "1.13" → right-pane wrap shifted JoinHorizontal pairing
- resize fixes layout temporarily → resize triggers a clean repaint,
  but the next scroll re-feeds bad bytes into the pipeline
- both terminals affected → corruption is upstream of the terminal

## Fix

`cvs.EnsureUTF8` (`cvs/encoding.go`):

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

Latin-1 maps 1:1 onto Unicode code points U+0000–U+00FF. Casting each
byte to a `rune` and re-encoding via `string([]rune)` produces correct
UTF-8 with no information loss. Pure ASCII and pure UTF-8 inputs hit
the `utf8.ValidString` fast path — no allocation.

Applied at every loader that pipes cvs output into a renderer:

| Loader              | What it returns       | Why it needs it |
|---------------------|-----------------------|-----------------|
| `loadRevisionContent` | single revision content | shown verbatim |
| `loadRevisionDiff`    | unified diff text       | shown verbatim, contains both file lines |
| `loadBlame`           | annotate output         | contains source lines |
| `loadPreviewDiff`     | working-copy diff       | tree variant B preview |
| Tree `f`-key preview  | `os.ReadFile` of file   | shown in dialog |

The `cvs log`, `cvs status`, and `cvs update` parsers don't need it —
their output is structured tokens (statuses, dates, revision numbers,
author names) that are reliably ASCII. Only the paths that pipe raw
file content to the renderer were affected, which is why the
artifacts concentrated in the History tab's right pane.

## Lessons

1. **"Resize fixes it" is not always a stale-state symptom.** It can
   also mean "resize triggers a code path that happens to produce
   different (correct) output for the same data." Distinguishing
   requires checking whether the *next* user action re-introduces
   the artifact.
2. **Width-counting bugs and content-encoding bugs look identical at
   the symptom layer.** Both produce "lines wider than the panel,
   text wraps in the terminal, layout shifts." The way to tell them
   apart is to look at *what character is wrong*, not *what's
   misaligned*. The `�` was the actual signal — it pointed at
   encoding the whole time.
3. **CVS predates the UTF-8 default.** Any CVS tool needs an opinion
   about non-UTF-8 file content. The simplest opinion that works for
   the common case is "valid UTF-8 stays as is, anything else is
   Latin-1," because Latin-1 is the most common pre-UTF-8 encoding
   in CVS-era European codebases and its transcoding is unambiguous.

## Tests

`cvs/encoding_test.go` covers:
- Valid UTF-8 passthrough (single-byte and multi-byte chars)
- Latin-1 single umlaut transcoding
- Latin-1 multiple umlauts + ß
- Empty string and pure ASCII fast paths
