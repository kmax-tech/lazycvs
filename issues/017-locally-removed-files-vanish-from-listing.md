# 017 — `cvs remove -f` files vanished from the listing

## Problem

A user pressed `D` on a tracked file (`figure-bayes-assessment.tex`),
confirmed the dialog, and the file disappeared from the right pane
entirely. They expected to see an `R`-status row with a `(server)`
marker reminding them to run `c` to commit the deletion.

The actual cvs invocation succeeded (`cvs remove -f` showed up in
the console log), the followup `cvs status -l` ran, but the file
never reappeared as an `R` entry. Effectively the user lost track
of the pending deletion until the next `s` from a clean state.

## Cause

`cvs status` for a removed-but-not-yet-committed file emits:

```
File: no file figure-bayes-assessment.tex	Status: Locally Removed
```

The literal text "no file" is CVS' way of saying the working copy
is gone. Our `statusFileRe` regex was:

```go
^File:\s+(\S+)\s+Status:\s+(.+)$
```

`\S+` greedy-matched the first non-whitespace token after `File:`,
which was the word `no`. So `FileStatus.Path` came back as `"no"`
and the actual filename was lost.

`statusMap["no"] = "R"` doesn't correspond to anything the file
listing or ServerOnly fallback knows about — both look up entries by
the real path. The removed file silently dropped out of the UI.

## Fix

Extended the regex with an optional `no file ` prefix:

```go
^File:\s+(?:no file\s+)?(\S+)\s+Status:\s+(.+)$
```

Added cvs/status_test.go covering both the removed-file format and a
regular file (regression guard). The `(?:...)?` non-capturing group
keeps the captured filename in `m[1]` whether or not the prefix is
present, so callers don't need to change.

After the fix, `cvs remove -f <file>` followed by the targeted status
refresh surfaces the file as `R` with a `(server)` marker — the user
can mark it and `c` to commit the deletion.

## Notes

CVS has a handful of other "no file" / "no rev" formats that the
parser doesn't currently exercise (e.g. `File: no file <name>		Status: Needs Checkout`
for files missing locally but expected by the server). If users hit
similar phantom-disappearance bugs, extend the same regex prefix to
cover them rather than re-inventing the parser.
