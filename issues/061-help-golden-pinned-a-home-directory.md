# 061 — The help golden pinned one machine's home directory

## Problem

`TestHelpGolden` failed on every machine but the one that recorded it:
the Console section of the help overlay names the session log, and the
golden carried that machine's absolute path
(`/Users/…/Library/Application Support/lazycvs/lazycvs.log`). On Linux,
or under another user, the path differs and so does the line's padding,
and `make check` went red without any change to the keymap.

## Cause

`helpSectionNote` reads `config.DefaultLogPath()` directly, which
resolves against the current user's config directory. The golden was
recorded with the real value baked in.

## Fix

The note reads the path through a package variable (`sessionLogPath`,
`tui/dialog.go`), which `TestHelpGolden` points at the placeholder
`<config-dir>/lazycvs.log` for the duration of the test. The golden was
regenerated with the placeholder; the live help still shows the real
path.
