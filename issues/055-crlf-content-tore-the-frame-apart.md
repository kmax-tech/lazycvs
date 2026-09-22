# 055 — A CRLF file tore the frame apart

## Problem

Opening the diff of `docs/crlf.txt` — a file with Windows line endings,
the normal case in any repository shared with Windows — destroyed the
display:

```
│v wc/  4M 1C                          ││@@ -1,2 +1,2 @@
                                                            │
                                                  │ile zwei geaendert
```

Panel borders overwritten, content of one pane printed into the other,
rows shifted. Not a parsing error: the frame itself came apart.

## Cause

A carriage return is not a character, it is an instruction: *move the
cursor to column 0*. The diff of a CRLF file carries one at the end of
every line, and the content went to the terminal unfiltered, so the
rest of each row was drawn over whatever the renderer had already put
on the left.

The same applies to every other C0 control character — escape above
all. Repository content could, in principle, repaint the screen.

## Fix

`cvs.DisplayText` is now the one way repository bytes reach the screen:
`EnsureUTF8` for the encoding, then `StripControl` for the steering.
Newline and tab stay (they belong in text); carriage return, escape,
backspace and the rest are dropped.

Order matters and is pinned by a test: transcode first, strip second —
Latin-1 bytes have to become runes before anything decides what counts
as a control character.

Applied at every loader that pipes content to a viewport: revision
content, revision diff, working diff, blame, and the Files tab's diff
pane.

## Files touched

- `cvs/encoding.go`, `cvs/encoding_test.go`
- `tui/history.go`, `tui/app_history.go` (call `DisplayText`)
