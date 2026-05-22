# 018 — `cvs status` Examining headers were on stderr; parser only read stdout

## Problem

After `cvs remove -f` on files in a subdir (e.g.
`ijcai26-fallacy-detection-appendix/figure-neutral-persona.tex`),
the resulting `R` rows appeared in the **parent** directory's
listing instead of attached to the subdir they came from. A `s`
refresh and a fresh ParseStatusInDir fallback didn't help — the
parser correctly handles the `cvs status` output when given the
right text, but the right text wasn't actually reaching it.

## Cause

`cvs status` interleaves two streams:

- **stderr**: `cvs status: Examining <dir>` progress headers, one
  per directory visited.
- **stdout**: `File: <name>  Status: …` blocks (one per tracked
  file), plus the `===` separators.

Our `CVSExecutor` captured stdout and stderr into separate
buffers, and the partition scan parsed only `result.Stdout`. The
"Examining" headers never reached `ParseStatusInDir`, so the
parser saw

```
=========================
File: foo.tex   Status: Locally Removed
   Working revision: -1.5
   Repository revision: 1.5  …
```

with no directory context. Its fallback (`defaultDir = "."`)
left every file path as the bare basename — `foo.tex` instead
of `subdir/foo.tex`. The `ServerOnly` fallback then placed
those `R` entries at the root level (1-part path → case 1),
not under the subdir's `SubDirGroup.Files` (case 2).

The bug was invisible to unit tests because we passed
synthetic "combined" output strings — the tests fed Examining
+ File lines together. The real flow split them.

## Fix

`CommandResult` gained a `Combined` field, populated via
`io.MultiWriter` against the same buffer for both stdout and
stderr in `cvs/executor.go::run`. cvs's actual write order is
preserved, so `Examining <dir>` lines now precede the File:
blocks they describe in the buffer.

The three `cvs status` callers (`refreshStagedFiles`,
`loadDirStatus`, `runPartitionScan`) now parse `result.Combined`
instead of `result.Stdout`. `ParseStatusInDir` already handles
the Examining lines correctly when they're present — they just
weren't reaching it before.

## Notes

The implication for other parsers: anything that depends on
cvs's progress messages needs to read `Combined`, not `Stdout`.
`ParseUpdate` already takes (stdout, stderr) separately — it's
fine as-is because it only consumes stderr for one-off warnings
(move-away, stale dirs) where order against stdout doesn't
matter.

Bench cost: each command now copies its output bytes twice
(once into the per-stream buffer, once into the combined). For
status/diff/log outputs this is sub-millisecond and irrelevant
relative to the cvs invocation itself.
