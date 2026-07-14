# 024 — Codebase audit: SSOT drift, unenforced ValidatePath, R-gate bug

## Problem

A single-source-of-truth audit found duplicated definitions that had
already drifted or were about to:

1. **ValidatePath had zero call sites.** CLAUDE.md documents "all
   handlers accepting file paths must call ValidatePath()" — the
   function existed in cvs/executor.go but nothing called it. A
   documented security guarantee that isn't enforced. It also had two
   latent bugs of its own: an unresolved WorkDir false-positives on
   macOS (where /tmp and /var are symlinks into /private), and the
   escape check used a plain string prefix, so a sibling dir named
   `<workdir>-evil` would have passed.
2. **The committable-status set existed five times, once wrong.**
   `isCommittable` (?, A, M, C, R) was the designated truth, but three
   call sites restated the list as literals and the Staged `c` gate
   dropped R — staging only scheduled deletions made `c` claim
   "nothing committable" while Enter would happily commit them.
3. `.lazycvs-backup` appeared as nine scattered literals (three local
   consts, five inline strings, one ignore pattern).
4. The pre-revert/pre-restore backup write existed three times
   (doRevert, doRestoreRev, stagedBulkPrepareBackups).
5. The bulk-revert goroutine loop existed twice (stagedBulkAction
   "revert" and the revertBulkMsg dialog handler) — one of them had
   already missed a semantic update once during the worklist change.
6. The CVS/Repository read + module-path normalization existed twice.

## Fix

- `validatePaths(exec, paths...)` is the enforcement helper; every
  action dispatcher that accepts working-copy paths calls it first:
  doCommit, doRemove, doRestoreRev, doRevert, doUpdatePaths,
  stagedBulkAction (add/update), addFile, revertPathsCmd, and the
  backup-restore flows. ValidatePath itself now resolves WorkDir
  symlinks and compares path-boundary aware; covered by a table test
  (traversal, absolute, symlink escape, prefix sibling).
- `StagedModel.CommittablePaths()` derives from `isCommittable`; all
  commit gates use it. The R-only staging case now commits from `c`.
- Package const `backupSuffix`; the default-ignore pattern derives
  from it (`"*"+backupSuffix`).
- `writeBackupFile` is the one backup writer; single-file flows log
  per file, bulk logs one summary.
- `revertPathsCmd` is the one bulk-revert dispatch, shared by the
  staged tab and the confirmation dialog.
- `moduleRoot(exec)` is the one CVS/Repository reader (extraction +
  recent-changes queries).
