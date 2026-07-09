# lazycvs User Guide

A keyboard-driven dashboard for CVS, modeled after lazygit. Wraps the
system `cvs` binary so you don't leave your editor's terminal for daily
operations.

## Getting started

```bash
make build
./lazycvs                       # opens the TUI in the current directory
./lazycvs ~/work/myproject      # explicit working copy
./lazycvs -cvs /opt/cvs/bin/cvs # alternate cvs binary
./lazycvs -config /tmp/cfg.toml # alternate config path
```

If the current directory isn't a CVS working copy, lazycvs falls back to
`[cvs] default_path` from your config file. If neither resolves to a CVS
checkout, lazycvs exits with an error.

### Bootstrap a new working copy: `lazycvs init`

Run `lazycvs init` inside an empty directory to bootstrap a fresh
checkout from a CVSROOT. The TUI prompts for the root, runs
`cvs co -c` to list available modules, and checks the picked one out
into the current directory. After a successful checkout it flows
straight into the normal session against the new working copy.

```bash
lazycvs init                                  # prompts for CVSROOT
lazycvs init :pserver:max@host:/srv/cvsroot   # skips the prompt, jumps to module list
```

CVSROOT resolution order: positional argument → `$CVSROOT` →
`[cvs] root` in the config → empty (dialog prompts).

Escape at any point aborts cleanly without leaving a half-checked-out
directory. Errors (bad root, auth failure, network) keep the dialog
open with the message visible so the user can edit & retry.

`lazycvs init` is the **only** entry into bootstrap mode — opening
lazycvs in an empty directory without `init` still errors out as
before; the bootstrap doesn't fire by accident.

#### Config options (optional)

```toml
[cvs]
root    = ":pserver:max@host:/srv/cvsroot"   # default CVSROOT for `init`
ssh_key = "/home/max/.ssh/cvs_id"            # see whitespace caveat below
```

`ssh_key` is a convenience for simple paths — it expands to
`CVS_RSH=ssh -i <path>` for the session. For paths with whitespace,
custom ports, or any non-trivial SSH config, set `CVS_RSH` yourself
and leave `ssh_key` unset.

Auth is assumed to be set up before `lazycvs init`: pserver via a
prior `cvs login`, ext/ssh via your usual ssh-agent or
`~/.ssh/config`. lazycvs does not prompt for passwords.

## Layout

```
┌─Tabs───────────────────────────────┐  Tabs: 1=Files 2=Fav 3=Staged 4=History
│ 1:Files | 2:Fav | 3:Staged | 4:Hist│
├─────────────────────────────────────┤
│ Left panel        │ Right panel    │  Two main panels per tab.
│ (tree / list)     │ (file list /   │
│                   │  diff /        │  Console below shows recent cvs
│                   │  commit form)  │  invocations + their durations.
├─────────────────────────────────────┤
│ Console (cvs invocation log)        │
├─────────────────────────────────────┤
│ Keybar — context-sensitive actions  │  Keybar always shows what's
└─────────────────────────────────────┘  available right now.
```

The console at the bottom logs every `cvs` command lazycvs runs, with
durations. A `⚠ N` indicator in the tab bar shows unread errors;
focusing the console with `Ctrl-J` clears it.

While a CVS command is in flight a yellow banner appears above the
tab bar (`⟳ Committing 3 file(s)…`, `⟳ Refreshing status…`, etc.).
When the command returns it turns green (`✓ Committed …`) or red
(`✗ Update failed — see Console`) for a couple of seconds, then
disappears. For silent actions (add, revert) the banner just
vanishes on success — disappearance is the "done" signal.

## Navigation — works the same everywhere

Vertical keys (`j`/`k`/arrows) move **inside** the focused pane.
Horizontal keys follow the **Miller-column model** known from
ranger/yazi — they move along one shallower ↔ deeper axis, and the
pane boundary is just a point on it: `→` digs deeper (expand a tree
dir; once there's nothing left to unfold, cross into the right pane),
`←` goes shallower (fold / walk up; from a right pane, step back into
the left one). `Ctrl` + the same keys always jump panes directly —
useful for the console, which sits outside the axis.

| Keys                  | Action                                            |
|-----------------------|---------------------------------------------------|
| `j` `k` `↓` `↑`       | Cursor up / down (within pane)                    |
| `l` `→`               | Expand tree dir; with nothing to expand, cross into the right pane |
| `h` `←`               | Collapse / walk up (tree); from a right pane, back to the left |
| `~`                   | Collapse all tree dirs, jump to root              |
| `g` `G`               | First / last item                                 |
| `PgUp` `PgDn`         | Page up / down                                    |
| `Ctrl-U` `Ctrl-D`     | Page up / down (vim alias)                        |
| `Ctrl-B` `Ctrl-F`     | Page up / down (vim alias)                        |
| `enter`               | Select + jump to right pane (yazi-style)          |
| `Ctrl-h` `Ctrl-←`     | Focus left pane                                   |
| `Ctrl-l` `Ctrl-→`     | Focus right pane                                  |
| `Ctrl-j` `Ctrl-↓`     | Focus console                                     |
| `Ctrl-k` `Ctrl-↑`     | Back to panes from console                        |
| `Tab` / `Shift-Tab`   | Cycle focus: left → right → console               |
| `?`                   | Help overlay                                      |
| `/`                   | Fuzzy search (jump to file or directory)          |
| `q`                   | Quit                                              |
| `Esc`               | Context-aware back / cancel                     |

Page sizes are scaled per panel — the History revisions list (two lines
per row) pages by half the height; everything else pages by full height.

## Tabs

### 1 — Files (Tree)

The default view. Left panel is a directory tree of every CVS-managed
folder under the working copy root, with status counts beside each
directory:

```
v project-root/  3M 2C 5?
  > docs/        2?
  > src/         3M 2C
  v tests/       3?
    > unit/
```

`M` = locally modified · `C` = conflict · `A` = added · `R` = scheduled
for removal · `?` = untracked · `U` = needs update from server.

Right panel shows the **file list** for whichever directory is selected
in the tree. It has three view modes (cycle with `f`):

- `flat` — files in the selected directory, plus every *changed* file
  from deeper subdirectories as a dir-relative path (clean deep files
  stay hidden)
- `sub`  — files grouped under each immediate subdirectory
- `tree` — expandable subdirs (press `Enter` on a subdir header to expand)

Changed files any number of levels down always get a row (attached to
their top-level subdirectory in `sub`/`tree` mode), so every count in a
directory badge has a selectable file behind it.

`F` filters by status code — each press cycles through:
all → `*` (anything changed: M/C/?/A/R/U) → `M` → `C` → `?` → all.
The active filter shows up in the file-list header (e.g. `[*]`).

`I` toggles whether ignored files (`.cvsignore`-matched and
default-ignored) are visible.

`.cvsignore` itself is a regular tracked file in lazycvs: it shows
up in the listing, you can edit it with `e`, and commits to it are
shared with the team (that's CVS' design). For personal-only
ignores, use `~/.cvsignore` via the `i` dialog options [2] / [3].

### 2 — Favorites

Pinned directories. Live status counts update with the rest of the
working copy. Useful when your repo has many top-level dirs but you
only care about a few.

`+` pins the directory under the cursor (from the Files tree or this
tab); `-` unpins the selected favorite. Both persist to the
`[favorites]` section of the config file, which you can also edit by
hand (e.g. to set a display name per entry).

### 3 — Staged

Files you've **marked** with `space`. Press `c` to open the commit
input; type a message and `Enter` commits everything in this tab as one
cvs invocation. `?` files get `cvs add`-ed automatically before commit.

`r` bulk-reverts every marked `M` and `C` file after a confirmation
dialog; `i` adds marked `?` files to `.cvsignore`; `u` runs
`cvs update` on the selection.

**Multi-line commit messages**: while the commit input is focused,
press `Ctrl+E` to open `$EDITOR` with the current message + a comment
block listing every file (with status). Save+quit commits; quit empty
aborts. Lines starting with `#` are stripped — same convention as
`git commit`.

### 4 — History

Per-file revision history with a side-by-side or unified diff viewer.
See [History tab](#history-tab-in-depth) below.

## File actions

These work in Tree, Favorites, and Staged tabs (with focus on either
panel). The selected file is the one under the cursor, or — when files
are marked with `space` — the entire marked set.

| Key       | Action                                                       |
|-----------|--------------------------------------------------------------|
| `space`   | Mark / unmark file. On a directory: toggle every changed file under it. |
| `A`       | Mark / unmark every changed file in the current directory subtree |
| `c`       | Commit. Marked set first; otherwise the file under the cursor. `?` files get `cvs add` automatically as part of the commit. |
| `r`       | Revert after confirmation — keeps a `.lazycvs-backup` copy. `M` files via `cvs update -C`; `C` files via delete + re-fetch (clears the conflict marker). Bulk over the whole staged set in the Staged tab. |
| `D`       | Remove. Marked set in a list dialog, otherwise single file. Untracked files just get deleted from disk; tracked files are scheduled for removal (status `R`). |
| `e`       | Open in `$EDITOR`                                            |
| `E`       | Open the configured external diff tool (working copy vs HEAD) |
| `M`       | Open the configured external merge tool (conflict files only) |
| `i`       | Add to `.cvsignore`. Marked `?` files → bulk; single → dialog with three options (local / global pattern / global exact). |
| `a`       | `cvs add` (`?` files only)                                   |
| `o`       | Open with the OS default viewer (`open` / `xdg-open`)        |
| `p`       | Read-only file preview overlay                               |
| `d`       | Jump to History tab pre-loaded with this file                |
| `+` / `-` | Pin the dir under the cursor as favorite / unpin the selected favorite (`-` in Favorites tab only) |
| `u`       | `cvs update` (selection or whole repo)                       |
| `U`       | Force update (`cvs update -C`)                               |
| `s`       | Refresh status (dry-run update + per-dir scan in parallel; progress in the banner) |

After every action, lazycvs runs `cvs status -l` on the affected
directories so the file list and status counts update without a manual
refresh.

## Fuzzy search (`/`)

Press `/` from any tab. A textbox opens; type a few characters of a
file or directory name and pick from the matches with `↑`/`↓`,
confirm with `Enter`, cancel with `Esc`.

Behavior on selection:

- **File picked** → Tree expands down to the file's parent directory,
  the tree cursor lands on that parent, the right-pane file list
  shows the directory's contents with the cursor on the picked file,
  and focus switches to the right panel so file actions
  (`d` / `e` / `c` / `D` / ...) just work.
- **Directory picked** → Tree expands down to the directory, cursor
  lands on it, focus stays on the left panel, the right pane shows
  the directory's contents.

Search uses the [`sahilm/fuzzy`](https://github.com/sahilm/fuzzy)
library against the working-copy file index that's built once on
first invocation; subsequent searches are instant.

## Marking workflow

1. Press `space` on each file you want to act on (or `A` to mark every
   changed file in the current directory + subdirs).
2. Status counts in the tab bar update: `Staged(3)` means three marked.
3. Switch to the **Staged** tab (`3`) to review the selection.
4. Press the action key:
   - `c` to commit (opens message input)
   - `r` to revert all marked `M` files
   - `i` to ignore all marked `?` files
   - `D` to remove (opens confirmation dialog listing each file)
   - `u` / `U` to update / force update

Marking persists across tab switches. Unmark a single file with `space`
again, or `A` on the directory to clear the whole subtree.

## Console & session log

The console panel at the bottom shows every command lazycvs ran —
both the real `cvs` invocations and the file operations it performs
itself (`rm` during a conflict revert, `mv` for backup restores and
in-the-way resolutions, backup copies before reverts). That makes it
the answer to "which files did that action actually delete?".

- `Ctrl-j` focuses the console; `Ctrl-k` goes back up
- `+` / `-` grow / shrink the panel (while focused)
- `F` cycles the filter: compact → verbose → errors → slow
- `x` clears the panel — the session log file below keeps everything
- The `⚠ n` badge in the tab bar counts failed commands since you last
  looked at the console

Everything is also appended to a rolling **session log file** next to
the config (e.g. `~/.config/lazycvs/lazycvs.log`, platform-dependent —
the help overlay `?` shows the exact path). Each lazycvs start writes a
`=== lazycvs session <time> — <workdir> ===` marker, successful
commands get one line each, failures include their error output. The
file can't grow unbounded: past ~1 MB it rotates to `lazycvs.log.old`
at the next start (one generation kept). Use it as an audit trail
across sessions:

```
=== lazycvs session 2026-07-09 16:18:01 — /home/me/webis ===
16:18:32 $ /opt/homebrew/bin/cvs status part-network-protocol (178ms) ok
16:19:02 $ cp 18 file(s) → *.lazycvs-backup  # pre-revert safety copies ok
16:19:03 $ rm webis24-figures/meyer.jpg  # revert conflict: refetch clean copy ok
```

## History tab in depth

Open with `4` (or `d` on a file in another tab). Left panel is the
revisions list, right panel is the diff or content viewer.

### Working copy as a row

The left panel exposes the working copy itself, depending on whether
it's modified:

**File is dirty (M / C / A / R):** a synthetic row appears at the top.
```
▌ working you      now
    local changes
  1.21    bmst2305 Apr 29 26
    *** empty log message ***  +11-11
  1.20    huvi7201 Apr 29 26
  ...
```
The row is fully cursor-navigable. With the cursor on it, the right
pane shows the working copy ↔ base diff. You can also pin it as the
compare anchor with `space`.

**File is clean:** no synthetic row; instead the row matching the on-disk
file's revision (sticky-tag aware — read from `cvs status`'s "Working
revision") gets a `(working)` badge:
```
  1.21    bmst2305 Apr 29 26   (working)
    *** empty log message ***  +11-11
  1.20    huvi7201 Apr 29 26
```

### Three view modes

| Key     | Mode    | Right pane shows                                |
|---------|---------|-------------------------------------------------|
| `d`     | Diff    | What changed in the cursor's revision (default) |
| `Enter` | Content | Full content of the cursor's revision           |
| `b`     | Blame   | `cvs annotate` (per-line revision attribution)  |

`p` toggles unified vs side-by-side diff in Diff mode. `</>` scrolls
horizontally. Mode keys work from either panel.

### Comparison

| Action                              | Result                                                  |
|-------------------------------------|---------------------------------------------------------|
| `space` on a revision               | Pin as compare anchor. Move cursor → diff anchor ↔ cursor |
| `space` on the same row             | Clear anchor                                            |
| `space` on the working pseudo-row   | Pin working as anchor                                   |
| `w`                                 | Toggle "compare against working copy" for the cursor row |
| `Esc`                               | Clear comparison; second `Esc` exits the History tab    |

Title in the right pane reflects the comparison: `Diff — 1.20 ↔ 1.21`,
`Compare — 1.18 ↔ 1.21`, or `Diff — 1.21 ↔ working`.

### Multi-file persistence

History is per-file. Each file you visit retains its own cursor, mode,
side-by-side toggle, and compare anchor. Switching to a different file
and back resumes the same view.

Revisions and diffs are cached per-file, so revisits are instant — the
cvs binary only runs the first time you visit each file (and once more
when the file is committed/updated, since the cache invalidates after
those actions).

## Conflict resolution

Files with status `C`:

- `M` opens the configured external merge tool (3-way: BASE / LOCAL /
  REMOTE / MERGED). Configure via `[editor] merge_tool = "meld"` or any
  preset (`kdiff3`, `vimdiff`, `vscode`, `opendiff`, `diffuse`,
  `bcompare`).
- The "Merge" dialog (also accessible via `M`) lets you pick local /
  remote / both for individual conflict regions if you don't want to
  spawn an external tool.
- Once resolved, `c` commits the merge. lazycvs blocks the commit if any
  marked file still contains `<<<<<<< / =======` markers and surfaces the
  reason in a banner.

## Backup files (`*.lazycvs-backup`)

Every revert (`r` on a single file or in the Staged tab) writes a
`<file>.lazycvs-backup` next to the file before overwriting it. The
backup is plain disk content — `mv` it back any time you want the
pre-revert state.

Inside lazycvs:

* The backup files are part of the default ignore list, so they
  don't clutter the file listing. Toggle `I` to see them.
* Cursor on either side of the pair + `B` swaps: lazycvs renames
  `<file>.lazycvs-backup` over `<file>`, the backup file disappears,
  and the restored content shows up as `M` ready to commit or
  revert again.
* `B` with files marked (`Space` on each, or `A` on a dir) restores
  the whole set at once — the typical undo after an accidental
  bulk revert. Paths that don't have a matching backup are skipped
  and reported in the result banner; the rest still go through.
  Toggle `I` first to show the `.lazycvs-backup` rows if you want
  to mark them directly.
* `B` with the **cursor on a directory in the tree pane** (no marks)
  walks the dir's subtree and restores every `.lazycvs-backup` it
  finds — the cleanest "undo all reverts I did under here" path.
  CVS/ admin dirs are skipped. Use this instead of `A` + `B` when
  you'd otherwise sweep up unrelated `?` files in the marking pass.

To clear them in bulk:

```bash
lazycvs clean-backups          # scrubs the current directory tree
lazycvs clean-backups -n       # dry-run: lists what would be removed
lazycvs clean-backups path/to  # scoped to a subtree
```

Skips `CVS/` administrative directories.

## Configuration

Default path on macOS: `~/Library/Application Support/lazycvs/config.toml`
Other platforms: `$XDG_CONFIG_HOME/lazycvs/config.toml`

```toml
[cvs]
binary       = "cvs"                       # cvs binary
default_path = "~/work/my-cvs-checkout"    # fallback when cwd isn't a CVS WC
timeout      = "60s"                       # per-command timeout

[editor]
editor       = "vim"                       # also reads $EDITOR
diff_tool    = "meld"                      # E key
merge_tool   = "meld"                      # M key (conflict files)

# Built-in presets for diff_tool / merge_tool: vscode, emacs, vimdiff,
# meld, kdiff3, opendiff, diffuse, bcompare. Or:
# diff_command  = "my-tool $LEFT $RIGHT"
# merge_command = "my-tool $BASE $LOCAL $REMOTE -o $MERGED"

[favorites]
dirs = ["src/main", "docs", "tests/integration"]
```

## Common workflows

**Daily check-in**
1. Start lazycvs in your working copy (`./lazycvs`)
2. `s` to refresh status
3. Browse changed files; `e` / `space` per file
4. `c` to commit marked

**Review what changed in a commit**
1. `4` (or `d` on the file) → History tab
2. Cursor on the commit's revision → Diff mode shows what changed in it
3. `p` for side-by-side if the diff is wide

**Diff working copy against an older release**
1. `4` → History
2. `space` on the older revision → anchor it
3. `w` (or arrow up to the working pseudo-row) → see anchor ↔ working

**Untracked-file cleanup**
1. `1` → Tree
2. `F` → filter by `?`
3. `A` to mark everything under cursor's directory
4. `i` to bulk-add to `.cvsignore`, OR `D` to remove from disk

**Conflict resolution**
1. `1` → Tree, look for `C` status
2. Move cursor onto the file
3. `M` to open the configured merge tool, or
4. `c` then resolve conflict markers in `$EDITOR`, then `c` again

**Discard local edits + conflicts in bulk** (take server's version for everything)

Stage the files you want to drop (Files tab + `Space` / `A`, or just
let the staged tab populate them), switch to `3:Staged`, and press
`r`. lazycvs runs `cvs update -C` over every M and C entry in the
staged set, replacing each file with the server's revision and
clearing conflict markers. A backup of each file's pre-revert
content lands next to it as `<name>.lazycvs-backup`. No
per-file confirmation prompt — the staged set IS the confirmation,
so unstage anything you want to keep before pressing `r`.

**Move files into a new subfolder** (CVS has no `mv`, so it's a remove + add)

If you reorganized your working copy by copying files from the root
(or another tracked dir) into a fresh subfolder, you'll see the
originals still listed at their old location with `M` (because they
were edited or because the working tree changed) and the new
subfolder won't show up in CVS at all until added. With identical
content on both sides the resolution is two commits:

1. `1` → Tree, cursor on the new subfolder → `a` to add the directory
2. Open the subfolder, `A` (mark all) → `a` to add the files
3. `c` → commit ("Move foo into subfolder/")
4. Cursor on the old root copies (the `M` rows) → `D` to remove
5. `c` → commit ("Remove foo from root after move")

Caveat: CVS resets the revision to `1.1` at the new path; the old
history stays accessible only at the old path (`cvs log Attic/...,v`
on the server). Copy the `,v` file server-side if you need history
at the new location.

## Troubleshooting

- **No revisions show in History** — file may be untracked (`?`) or
  newly added (`A`) without commits yet.
- **`untersuch��` or replacement chars in diff** — file is encoded in
  Latin-1; lazycvs detects this and transcodes to UTF-8 automatically
  (issue 011).
- **Status counts look stale** — press `s`. lazycvs auto-refreshes
  affected directories after every action, but a manual refresh covers
  external `cvs` invocations done outside lazycvs.
- **Merge tool dialog says "no merge tool configured"** — set `[editor]
  merge_tool` in your config file.
- **Wrong CVS binary** — pass `-cvs /path/to/cvs` or set `[cvs] binary`
  in config.

For deeper details on how lazycvs works internally, see
[`docs/ARCHITECTURE.md`](ARCHITECTURE.md).
