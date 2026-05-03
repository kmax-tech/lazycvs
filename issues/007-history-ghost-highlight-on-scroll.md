# 007 — Ghost highlighted rows and content above header when scrolling history

## Problem

Scrolling through revisions in the History tab caused visual corruption:
multiple rows appeared highlighted simultaneously, and revision content
sometimes rendered above the header line. The first render was correct;
artifacts appeared after the first cursor movement.

## Cause

`ViewLeft()` applied `lipgloss.NewStyle().Reverse(true)` to `line1` and
`line2` which already contained inner styled segments — `mutedStyle.Render()`,
`colorUpdated`, `colorConflict`, `colorStale`, `colorActive`. Each inner
`Render()` call appends `\x1b[0m` (SGR full reset), which cancels the outer
`\x1b[7m` (reverse video). The result:

- Reverse video only applied to the portion before the first inner style reset.
- Trailing content and padding spaces rendered in normal mode.
- Bubble Tea's differential renderer saw inconsistent ANSI state between frames,
  leaving stale reverse-video fragments on screen from previous cursor positions.

## Fix

Two changes in `ViewLeft()` for the cursor row:

1. **Strip inner ANSI** before applying `Reverse(true)` — `ansi.Strip()`
   removes all escape sequences so the outer `\x1b[7m...\x1b[0m` has no inner
   resets to fight.

2. **Pad to full panel width** before wrapping with Reverse — padding spaces
   are included inside the reverse-video wrapper so the highlight fills the
   entire row. Without this, `renderPanel()` adds padding *after* the
   `\x1b[0m` reset, leaving un-reversed trailing spaces that the differential
   renderer treats differently between frames.

```go
plain1 := ansi.Strip(line1)
plain2 := ansi.Strip(line2)
if w := lipgloss.Width(plain1); w < m.leftWidth {
    plain1 += strings.Repeat(" ", m.leftWidth-w)
}
line1 = sel.Render(plain1)
```

The cursor row loses per-segment colors (tag, delta, muted message) but the
selection highlight itself provides the visual distinction.
