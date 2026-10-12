# lazycvs User Guide

A keyboard-driven dashboard for CVS, modeled after lazygit. Wraps the
system `cvs` binary so you don't leave your editor's terminal for daily
operations.

## Getting started

Install via `go install github.com/kmax-tech/lazycvs@latest` or build a
clone with `make build` (see the README's Install section). Then:

```bash
lazycvs                       # opens the TUI in the current directory
lazycvs ~/work/myproject      # explicit working copy
lazycvs -cvs /opt/cvs/bin/cvs # alternate cvs binary
lazycvs -config /tmp/cfg.toml # alternate config path
lazycvs -follow /tmp/nav.fifo # selection follows paths written to a FIFO
```

**Follow mode** (`-follow <fifo>`) is for running lazycvs next to a file
manager: the other program creates the FIFO (`mkfifo`) and writes absolute
paths into it, one per line; lazycvs moves its selection there, the same
jump the fuzzy finder makes. A path waits while you are busy (a dialog or
the search is open, another tab is active) and lands as soon as you are
back on the Tree tab; paths outside the working copy are ignored. A path
that is not a FIFO exits with an error; if reading stops mid-session, the
console says so. conspectus uses it for its terminal panel.

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
┌─Tabs───────────────────────────────┐  Tabs: 1=Files 2=Fav 3=Marked 4=History
│ 1:Files | 2:Fav | 3:Marked | 4:Hist│
├─────────────────────────────────────┤
│ Left panel        │ Right panel    │  Two main panels per tab.
│ (tree / list)     │ (file list /   │
│                   │  diff /        │
│                   │  commit form)  │
├─────────────────────────────────────┤
│ Console — one line unless focused   │  Expands on focus (Ctrl-J) and
├─────────────────────────────────────┤  whenever a command fails.
│ Nav keys (same everywhere)          │
│ Keys that apply to the current row  │
└─────────────────────────────────────┘
```

The keybar's second line is not a catalogue: it lists the keys that
apply to the row under the cursor. An untracked file offers `a` and
`i`, a conflict offers `M`, a clean file offers neither. With files
marked, the line starts with `3 marked ▸` — because the marks, not the
cursor, are then what the actions address. When the keys do not fit the
terminal, the least important go first and the row ends in `+7 (?)`
rather than being cut mid-word; `?` lists all of them.

The tab bar's right-hand side says where you are: the module, the
sticky tag of the directory the file pane is showing, and how long ago
the whole tree was last checked against the server.

```
 1:Files | 2:Fav | 3:Marked | 4:History        demo · release-2 · ⟳ 3m
```

The branch is the reason this exists. CVS pins stickiness **per
directory**: `cvs update -r release-2` inside one subdirectory leaves
the rest of the tree on the trunk, and nothing in a file listing tells
you which one you are looking at. A directory with no sticky tag reads
as `MAIN`, in grey; a sticky one is yellow, and a *non-branch* tag —
one you cannot commit to — adds `no-commit`, which is cheaper to read
here than in cvs's refusal after you have written the code.

The age (`⟳ 3m`) counts from the last whole-tree check: startup, `s`,
or a refresh after an action. Per-directory scans do not reset it, or
it would always read "now" and answer nothing. On a narrow terminal the
age goes first and the module second; the branch stays.

The console at the bottom logs every `cvs` command lazycvs runs, with
durations. It stays collapsed to a single line — the command that just
ran — until you focus it (`Ctrl-J`) or a command fails, and it collapses
again once you have looked. `+` / `-` set its expanded height while it
has focus; `[ui] console_height` sets the default.

A `⚠ N` indicator in the tab bar shows unread errors. `Ctrl-J` right
after a failure opens the console **on the errors filter**, so the
failure is the first thing in it; the red banner says so too.

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
| `?`                   | Help overlay, opened at the section for where you are (History tab → History keys, console focused → Console keys). `/` filters the list by key or description; `Esc` clears the filter, `Esc` again closes. |
| `/`                   | Fuzzy search (jump to file or directory)          |
| `q`                   | Quit. With files marked or a commit message typed, the first `q` warns and a second one quits; any other key cancels. |
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

Press `d` on a file to see **your changes to it** in the right pane:
the tree switches to showing files, the cursor stays on the same file,
and `j`/`k` then walks from file to file with the diff following. `d`
again (or `Esc`) brings the file list back, cursor still on that file.
The tab title says `Diff` while that layout is open.

Right panel shows the **file list** for whichever directory is selected
in the tree. It has two views, toggled with `f`, because there are two
questions:

- `flat` — *what changed here?* The selected directory's files, plus
  every **changed** file from deeper subdirectories as a dir-relative
  path. Clean files further down stay hidden.
- `tree` — *let me walk the directories.* Subdirectories as foldable
  rows; `Enter` opens one, `~` folds or unfolds all of them at once.
  The fold state sticks, so the next directory opens the way this one
  does — the header says `tree · all open` when it is unfolded.

Changed files any number of levels down always get a row (attached to
their top-level subdirectory in the tree view), so every count in a
directory badge has a selectable file behind it.

`F` filters by status code — each press cycles through:
all → `*` (anything changed: M/C/?/A/R/U) → `M` → `C` → `?` → all.

The file-list header names the state it is in — view mode, whether
ignored files are hidden, the active filter — and the count of files it
is showing:

```
docs/ · flat · ignored:hidden · filter:M — 3 files, 2 marked
```

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

### 3 — Marked

Files you've marked with `space` — the worklist `c`, `r`, `i` and `u`
act on. CVS has no staging area; this tab is a selection, not an index,
and opening it never changes what is in it.

Press `c` to open the commit input; type a message and `Enter` commits
everything in this tab as one cvs invocation. `?` files get
`cvs add`-ed automatically before commit.

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

These work in Tree, Favorites, and Marked tabs (with focus on either
panel). The selected file is the one under the cursor, or — when files
are marked with `space` — the entire marked set.

| Key       | Action                                                       |
|-----------|--------------------------------------------------------------|
| `space`   | Mark / unmark file. On a directory: toggle every changed file under it. |
| `A`       | Mark / unmark every changed file in the current directory subtree |
| `c`       | Commit. Marked set first; otherwise the file under the cursor. `?` files get `cvs add` automatically as part of the commit. |
| `r`       | Revert after confirmation — keeps a `.lazycvs-backup` copy. `M` files via `cvs update -C`; `C` files via delete + re-fetch (clears the conflict marker). With marked files: one bulk confirmation over every revertable (`M`/`C`) marked path; without marks: the file under the cursor. |
| `D`       | Remove. Marked set in a list dialog, otherwise single file. Untracked files just get deleted from disk; tracked files are scheduled for removal (status `R`). |
| `e`       | Open in `$EDITOR`                                            |
| `E`       | Open the configured external diff tool (working copy vs HEAD) |
| `M`       | Open the configured external merge tool. On a `C` file: the conflict's BASE/LOCAL/REMOTE. In the History tab: merge the selected revision pair into the working copy. |
| `i`       | Add to `.cvsignore`. Marked `?` files → bulk; single → dialog with three options (local / global pattern / global exact). |
| `a`       | `cvs add` (`?` files only)                                   |
| `o`       | Open with the OS default viewer (`open` / `xdg-open`)        |
| `Ctrl-e`  | Alias of `e` — same key that fzf/yazi-style pickers use for "open in editor" |
| `Ctrl-o`  | Open with the configured alternate opener (`[editor] alt_open`, e.g. a GUI editor) — launched detached, the TUI keeps running |
| `Ctrl-r`  | Reveal in the file manager (Finder `open -R`; elsewhere the containing dir) |
| `Ctrl-y`  | Copy the file's absolute path to the clipboard               |
| `d`       | Show your changes to this file in the pane beside it (Files tab layout); `d` again or `Esc` closes it |
| `+` / `-` | Pin the dir under the cursor as favorite / unpin the selected favorite (`-` in Favorites tab only) |
| `u`       | `cvs update` (selection or whole repo)                       |
| `U`       | Force update (`cvs update -C`)                               |
| `H`       | Recent repository changes for the dir under the cursor, subdirs included: who committed/added/removed which file (`cvs history -x AMR -a`, all users). `Enter` on a row jumps to that file's History tab (revisions, diffs, commit messages). The events are kept in a **persistent local history** (a JSON store next to the config, keyed by CVSROOT and shared between checkouts of the same repo): every query — including the first one after a restart — only fetches the delta since the last fetch and merges it in. Within 5 minutes re-pressing `H` anywhere is instant with no server contact at all; the header shows the fetch age and `r` inside the dialog forces a delta refresh. Only the very first query ever (or widening `history_days` beyond what the local store covers) pulls a full window. Window defaults to 7 days — set `history_days` under `[cvs]` in the config. Needs history logging on the server (`LogHistory` in `CVSROOT/config`). |
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

**The model: marks are a worklist.** You build a set of files, run one
bulk action on it, and the action *consumes* the set when it succeeds.

1. Press `space` on each file you want to act on (or `A` to mark every
   changed file in the current directory + subdirs).
2. The tab bar counts them: `Marked(3)` means three marked, and the
   keybar's action line starts with `3 marked ▸`.
3. Switch to the **Marked** tab (`3`) to review the selection. Opening
   it changes nothing — looking at a worklist must not edit it.
4. Press the action key:
   - `c` to commit (opens message input)
   - `r` to revert all marked `M`/`C` files (confirmation dialog)
   - `i` to ignore all marked `?` files
   - `D` to remove (opens confirmation dialog listing each file)
   - `u` / `U` to update / force update

The worklist lifecycle after an action:

- **Success** → the acted-on paths are unmarked automatically; the
  worklist is done.
- **Failure** → the marks stay, so you can fix the problem (see the
  Console) and retry the same selection.
- **Exception `a` (add)** → marks are kept even on success: adding is
  the preparation step, the follow-up commit is the action that
  consumes the set.

Marking persists across tab switches. Unmark a single file with
`space`, a subtree with `A` on its directory, or **everything at once
with `x`** (works in any tab; with the console focused, `x` clears the
command log instead).

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
| `E`                                 | Open the selected pair in the external diff tool        |
| `M`                                 | Merge the selected pair into the working copy           |

Title in the right pane reflects the comparison: `Diff — 1.20 ↔ 1.21`,
`Compare — 1.18 ↔ 1.21`, or `Diff — 1.21 ↔ working`.

`E` and `M` act on exactly the pair the title names — anchor 1.85, cursor
on 1.90, and both tools see those two revisions. Without an anchor the
pair is the cursor's revision and its parent.

### Merging two revisions into the working copy

`M` in the History tab is the interactive equivalent of
`cvs update -j <from> -j <to>`: it feeds the change between the two
selected revisions to your merge tool instead of writing conflict
markers into the file.

| Merge-tool side | What it gets                                     |
|-----------------|--------------------------------------------------|
| `$BASE`         | the "from" revision (anchor, or cursor's parent)  |
| `$REMOTE`       | the "to" revision (cursor)                        |
| `$LOCAL`        | a snapshot of the working copy as it is right now |
| `$MERGED`       | the working file itself — the tool writes here    |

The result lands in the working copy as an ordinary local modification,
so you review it and commit it like any other change. A
`.lazycvs-backup` copy is written before the tool starts (`B` restores
it), because handing an external tool write access to the working copy
is destructive by the same standard as `r` and `R`.

Both sides must be real revisions. With `w` active — the working copy
pinned into the comparison — `M` refuses: the working copy is already
the `$LOCAL` side.

An unresolved conflict outranks the revision merge: if the file itself
has status `C`, `M` opens the conflict's BASE/LOCAL/REMOTE instead —
CVS's pre-merge backup makes a better `$LOCAL` than any snapshot, and
the conflict is the more urgent thing to fix. Resolve it first, then
merge revisions.

Note what this is *not* good for: encoding damage. A merge between two
revisions of the same linear history has the older one as its common
ancestor, so the merge degenerates to "take the newer". Repairing a
botched charset conversion is a byte transformation (`iconv`), not a
merge.

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
  preset (`kdiff3`, `vimdiff`, `nvimdiff`, `vscode`, `opendiff`,
  `bcompare`).
- The "Merge" dialog (also accessible via `M`) lets you pick local /
  remote / both for individual conflict regions if you don't want to
  spawn an external tool.
- Once resolved, `c` commits the merge. lazycvs blocks the commit if any
  marked file still contains `<<<<<<< / =======` markers and surfaces the
  reason in a banner.

## Backup files (`*.lazycvs-backup`)

Every revert (`r` on a single file or in the Marked tab) writes a
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
expected_encoding = "latin1"               # repo text encoding ("latin1" or
                                           # "utf8"). Steers the yellow "!"
                                           # encoding warning in the file
                                           # list only — commit/diff are
                                           # never affected.

[ui]
tree_width    = 0                          # left panel width in columns.
                                           # 0 = a third of the terminal,
                                           # clamped to 25–40 (the old
                                           # fixed behaviour). A set value
                                           # never takes more than half
                                           # the screen.
console_height = 6                         # height of the console *when
                                           # expanded*. It collapses to one
                                           # line when unfocused with
                                           # nothing to report.

[editor]
editor       = "vim"                       # also reads $EDITOR
diff_tool    = "meld"                      # E key
merge_tool   = "meld"                      # M key (conflicts + revision pairs)
alt_open     = "emacsclient -n"            # Ctrl-o: alternate opener, launched
                                           # detached. $FILE marks where the
                                           # path goes; without it the path is
                                           # appended. E.g. "code --goto $FILE"

# Built-in presets for diff_tool: vscode, emacs, vimdiff, nvimdiff,
# meld, kdiff3, opendiff, diffuse, bcompare. For merge_tool: the same
# minus emacs and diffuse (no 3-way output file). Terminal tools
# (vimdiff, nvimdiff) suspend the TUI while open; GUI tools launch
# detached. The launch mode is inferred from the binary name — override
# it for wrapper scripts or tools the inference doesn't know:
# diff_terminal  = true   # force suspend-TUI launch for the diff tool
# merge_terminal = true   # same for the merge tool
# Or:
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

Mark the files you want to drop (Files tab + `space` / `A`), switch to
`3:Marked`, and press `r`. lazycvs runs `cvs update -C` over every M and
C entry in the marked set, replacing each file with the server's
revision and clearing conflict markers. A backup of each file's
pre-revert content lands next to it as `<name>.lazycvs-backup`. No
per-file confirmation prompt — the marked set IS the confirmation, so
unmark anything you want to keep before pressing `r`.

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
