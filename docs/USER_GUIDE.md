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

## Navigation — works the same everywhere

| Keys                | Action                                          |
|---------------------|-------------------------------------------------|
| `j` `k` `↓` `↑`     | Line by line                                    |
| `g` `G`             | First / last item                               |
| `PgUp` `PgDn`       | Page up / down                                  |
| `Ctrl-U` `Ctrl-D`   | Page up / down (vim alias)                      |
| `Ctrl-B` `Ctrl-F`   | Page up / down (vim alias)                      |
| `Tab` / `Shift-Tab` | Cycle focus: left panel → right panel → console |
| `[` `]` `Ctrl-J`    | Jump focus directly to left / right / console   |
| `?`                 | Help overlay                                    |
| `/`                 | Fuzzy search                                    |
| `q`                 | Quit                                            |
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

- `flat` — only files in the selected directory
- `sub`  — files grouped under each immediate subdirectory
- `tree` — expandable subdirs (press `Enter` on a subdir header to expand)

`F` filters by status code. `I` toggles whether ignored files are
visible.

### 2 — Favorites

Pinned directories (set in `[favorites]` config). Live status counts
update with the rest of the working copy. Useful when your repo has
many top-level dirs but you only care about a few.

### 3 — Staged

Files you've **marked** with `space`. Press `c` to open the commit
input; type a message and `Enter` commits everything in this tab as one
cvs invocation. `?` files get `cvs add`-ed automatically before commit.

`r` reverts marked `M` files; `i` adds marked `?` files to `.cvsignore`;
`u` runs `cvs update` on the selection.

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
| `r`       | Revert (`cvs update -C` — keeps a `.lazycvs-backup` copy)     |
| `D`       | Remove. Marked set in a list dialog, otherwise single file. Untracked files just get deleted from disk; tracked files are scheduled for removal (status `R`). |
| `e`       | Open in `$EDITOR`                                            |
| `E`       | Open the configured external diff tool (working copy vs HEAD) |
| `M`       | Open the configured external merge tool (conflict files only) |
| `i`       | Add to `.cvsignore`. Marked `?` files → bulk; single → dialog with three options (local / global pattern / global exact). |
| `a`       | `cvs add` (`?` files only)                                   |
| `o`       | Open with the OS default viewer (`open` / `xdg-open`)        |
| `p`       | Read-only file preview overlay                               |
| `d`       | Jump to History tab pre-loaded with this file                |
| `u`       | `cvs update` (selection or whole repo)                       |
| `U`       | Force update (`cvs update -C`)                               |
| `s`       | Refresh status (silent dry-run update)                       |

After every action, lazycvs runs `cvs status -l` on the affected
directories so the file list and status counts update without a manual
refresh.

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
