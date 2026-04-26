# lazycvs

A lazygit-inspired terminal UI for CVS, written in Go.

Wraps the `cvs` CLI so daily workflows — status, diff, commit, conflict
resolution, history, blame — happen in a fast keyboard-driven dashboard
instead of disjoint shell commands.

## Quick start

```bash
make build          # builds ./lazycvs
./lazycvs           # opens the TUI in the current working copy
```

`lazycvs` looks at your current directory first; if it isn't a CVS working
copy and you set `[cvs] default_path` in your config, it falls back there.

## Try it without a real repo

The repo ships a self-contained demo that creates a local CVS repository,
populates it with revisions and a real merge conflict, and launches `lazycvs`
against it — no server required.

```bash
make demo           # build + setup + launch
make demo-setup     # setup only (inspect with cvs CLI first)
make demo-clean     # tear down
```

The demo working copy ends up under `_lazycvs-demo/wc/` and includes one
file in every status the TUI cares about: `M`, `?`, `A`, `C`, `R`, plus
multi-revision history for the History tab.

## Configuration

Default path on macOS: `~/Library/Application Support/lazycvs/config.toml`
(other platforms: `$XDG_CONFIG_HOME/lazycvs/config.toml`).

```toml
[cvs]
binary       = "cvs"
default_path = "~/work/my-cvs-checkout"   # used when cwd has no CVS metadata

[editor]
diff_tool  = "meld"   # 2-way diff for E key
merge_tool = "meld"   # 3-way merge for M key (only on conflict files)
```

Built-in diff/merge presets: `vscode`, `emacs`, `vimdiff`, `meld`, `opendiff`,
`kdiff3`, `diffuse`, `bcompare`. Or set `diff_command` / `merge_command` to a
custom template using `$LEFT`/`$RIGHT` (diff) or `$BASE`/`$LOCAL`/`$REMOTE`/`$MERGED`
(merge) placeholders.

## Tabs

| Key | Tab | Purpose |
|---|---|---|
| `1` | Files | Tree + file list, status markers, mark for commit |
| `2` | Favorites | Pinned directories with live status counts |
| `3` | Staged | Files marked with `space`; `c` opens commit input |
| `4` | History | Revisions + working copy as a synthetic top row |

## Key actions on a file

| Key | Action |
|---|---|
| `space` | mark / unmark for commit |
| `c` | commit (universal — `?` files get cvs-add first) |
| `r` | revert (`cvs update -C`, with backup) |
| `D` | remove (`cvs remove -f`) |
| `e` | open in `$EDITOR` |
| `E` | open in configured external diff tool |
| `M` | open in configured external merge tool (conflict files only) |
| `i` | add to `.cvsignore` |
| `d` | open History tab pre-loaded with this file |
| `u` | `cvs update` |

The keybar at the bottom always shows what's available in the current
context. `?` opens the help overlay with the full keymap.

## Project layout

- `main.go` — entry point + CLI args + working-copy resolution
- `cvs/` — pure CVS data layer (string in, struct out)
- `tui/` — Bubble Tea TUI (lazygit-style panels)
- `config/`, `fs/` — supporting packages
- `scripts/demo.py` — standalone CVS demo generator

## Build

```bash
make build              # local binary
make release            # cross-compile for linux/mac/windows under dist/
make clean              # remove ./lazycvs and dist/
go test ./...           # run unit tests
```

Go 1.22+ required. Stdlib + a small set of `charmbracelet` libraries for
the TUI; CVS interaction goes through `os/exec` against the system `cvs`
binary.
