# 041 — Silent refusals, an unadvertised key, an unguarded quit

## Problem

Four places where the UI knew something and did not say it:

1. **`u` in the History tab did nothing, silently.** The refusal is
   right — History is read-only with respect to the working copy — but
   a key that neither acts nor explains reads as a broken app. That is
   the complaint issue 021 collected about dead keys, still live here
   as a deliberate one.
2. **`p` opened a preview modal that nothing advertised.** It appeared
   in neither the keybar nor the help, and it collided with
   `keys.SideBySide`: both bound `p` with identical help text, so the
   dispatcher could not tell them apart either. It was the sole entry in
   the registry's `pendingRemoval` list (issue 037).
3. **`q` quit at once, whatever was pending** — a twenty-file marked
   worklist, a half-typed commit message. Both are state the process
   exit destroys and nothing reconstructs.
4. **"see Console" left the user to find it.** Failure banners pointed
   at the console; reaching it meant focusing the panel and then
   cycling its filter past compact and verbose to errors, through
   however many successful commands had run since.

## Fix

1. `u` in History answers: *"History is read-only — press 1 to update
   files"*, on the same banner every other refusal uses.
2. The `p` binding, its handler and its keyMap field are gone.
   `DialogPreview` itself stays: it has become the generic
   text-display modal ("no diff tool configured"), which is a different
   thing from the key that once opened it. `pendingRemoval` is now
   empty — the healthy state for that list.
3. `q` with unsaved work warns and asks for a second `q`:
   *"2 file(s) marked — press q again to quit"*. Any other key cancels
   the pending quit, because it means the user went back to work.
   Deliberately not a modal: an "are you sure" dialog on every quit
   would cost more than the loss it prevents.
4. `C-j` after a failure opens the console **on the errors filter**
   (`ConsoleModel.ShowErrors`), and the failure banner names that route
   itself: `(C-j: console, errors only)`.

Point 4 deviates from the plan, which had `Enter` on the banner do
this. `Enter` is the pane's own key — select, open, cross into the
right pane — and stealing it for the seconds a banner is up would
misfire exactly when the user is working fastest. `C-j` is already
"go to the console"; it only learned where to land.

Tests: `TestUpdateInHistoryExplainsItself`, `TestQuitGuardsUnsavedWork`
(clean quit, marks, unsent message, and the cancel-on-any-other-key
rule), `TestConsoleOpensOnErrorsAfterAFailure` (including that a
console visit without a failure leaves the filter alone).

## Files touched

- `tui/app_keys.go` (History `u`, quit guard, preview handler removed)
- `tui/app_core.go` (`unsavedWork`, `bannerIsFailure`,
  `focusConsoleOnErrors`, `quitPending` and its reset)
- `tui/console.go` (`ShowErrors`), `tui/app_layout.go` (banner hint)
- `tui/keys.go`, `tui/keymap.go` (`keys.Preview` gone)
- `tui/app_keys_test.go`
