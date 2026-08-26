# 027 — External diff/merge launch: paths with spaces split argv, errors vanished

Follow-up hardening of the E/M external-tool launch (see
[026](026-terminal-difftool-detached.md)).

## Problem

1. A file named `my notes.bib` broke the external diff: the tool received
   two arguments (`…/my` and `notes.bib`) instead of one path.
2. When the configured tool binary was missing (e.g. `meld` not
   installed), pressing `E`/`M` did nothing visible — the key read as
   dead.

## Cause

1. `launchExternalDiff`/`launchExternalMerge` substituted `$LEFT`/`$RIGHT`
   etc. into the command template *first* and then split the whole string
   with `strings.Fields` — so spaces inside substituted paths became argv
   boundaries.
2. `externalDiffMsg.err` had no consumer in the update loop; launch
   failures were dropped on the floor by design ("fields aren't used by
   the model today").

## Fix

1. `expandTemplate` (tui/external.go) splits the template into tokens
   first and substitutes placeholders per token — paths keep their spaces.
   Placeholders embedded in larger tokens (emacs ediff preset) still work.
2. `app_core.go` now consumes `externalDiffMsg`: err → red result-bar
   message; success stays silent (the tool appearing is the feedback).

Also in this change: the terminal-vs-detached launch mode (026) became
overridable via `[editor] diff_terminal` / `merge_terminal` for wrapper
scripts and terminal tools the binary-name inference doesn't know.
