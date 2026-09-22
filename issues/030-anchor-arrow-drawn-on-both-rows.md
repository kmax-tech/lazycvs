# 030 — Compare anchor drew two stacked triangles

## Problem

Setting the compare anchor with `space` in the History tab painted the
`▸` arrow twice — once on the revision header row and once on the commit
message row directly below it. Two triangles stacked on top of each
other read as two anchors rather than one marked revision.

## Cause

Each entry in the revision list is rendered as two rows (`line1` header,
`line2` message). `ViewLeft` computed a single `renderedMarker` and
wrote it into both. For the cursor that is intentional — the `▌` bar
spanning both rows reads as one continuous block — but the anchor's
arrow is a point marker for one revision, so duplicating it was wrong.

## Fix

Split the single marker into `markerTop` / `markerBottom` in
`tui/history.go`:

- cursor → `▌` on both rows (unchanged, deliberate)
- anchor → `▸` on the header row only, blank below

Tests in `tui/external_test.go` assert exactly one `▸` in the rendered
list and that it sits on the row carrying the revision number, plus a
companion test pinning the cursor bar's two-row span so the fix cannot
be "simplified" into breaking it.
