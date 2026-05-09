# lazycvs Architecture

How the code is organized, which modules do what, and how the pieces
fit together. Companion to [`USER_GUIDE.md`](USER_GUIDE.md).

## Big picture

```
┌──────────────────────────────────────────────────────────────────┐
│                       main.go (entry point)                       │
│   CLI flags · resolve working copy · build CVSExecutor · run TUI │
└────────────────────────────┬─────────────────────────────────────┘
                             │
              ┌──────────────┴──────────────┐
              │                             │
        ┌─────▼──────┐               ┌──────▼──────┐
        │   tui/     │               │    cvs/     │
        │  TUI layer │               │  data layer │
        │ (Bubble    │  produces     │ (parsers +  │
        │  Tea)      │ ◀────────────│  executor)   │
        │            │   structs     │             │
        └─────┬──────┘               └──────┬──────┘
              │                             │
              │                             ├── parser.go    (cvs update)
              │                             ├── status.go    (cvs status)
              │                             ├── log.go       (cvs log)
              │                             ├── diff.go      (cvs diff)
              │                             ├── annotate.go  (cvs annotate)
              │                             ├── conflict.go  (merge markers)
              │                             ├── executor.go  (subprocess)
              │                             └── encoding.go  (Latin-1 → UTF-8)
              │
              ├── app_core.go       (App struct, msg routing)
              ├── app_layout.go    (panel layout, frame rendering)
              ├── app_keys.go      (global key dispatch)
              ├── app_files.go     (file list, ignore patterns)
              ├── app_actions.go   (commit/update/revert/add)
              ├── app_history.go   (History tab logic + caches)
              ├── app_status.go    (cvs status dispatch + parsing)
              ├── app_mouse.go     (mouse events → click/scroll)
              ├── tree.go          (TreeModel — left pane of Tree tab)
              ├── filelist.go      (FileListModel — right pane)
              ├── favorites.go     (FavoritesModel)
              ├── staging.go       (StagedModel)
              ├── history.go       (HistoryModel)
              ├── console.go       (ConsoleModel)
              ├── dialog.go        (DialogModel — overlays)
              ├── search.go        (SearchModel — fuzzy file finder)
              ├── external.go      (external diff/merge launchers)
              ├── scroll.go        (ensureCursorVisible + clamp)
              └── styles.go        (lipgloss style constants)
```

## Layering rule

The strict separation is **`cvs/` produces structs, `tui/` is the only
renderer**. The CVS layer never produces ANSI-colored strings or
formatted output; it consumes the textual stdout/stderr of the `cvs`
binary and returns Go structs (`UpdateResult`, `FileStatus`,
`FileHistory`, `DiffResult`, ...). The TUI reads those structs and
decides how to display them.

This means:
- The `cvs/` layer is fully unit-testable with no terminal dependency.
- The TUI layer can be swapped or extended without rewriting CVS parsing.
- Bug-hunting splits cleanly: parsing bugs land in `cvs/` tests; layout
  bugs stay confined to `tui/`.

## The CVSExecutor

All `cvs` invocations go through one `*cvs.CVSExecutor`
(`cvs/executor.go`). Important properties:

- **One mutex per executor** — `Run()` takes an exclusive lock,
  `RunReadOnly()` (status / diff / log / annotate) takes a shared lock.
  Writes can't race reads or each other; reads can run concurrently.
- **Configurable timeout** (default 60s) via `context.WithTimeout`.
- **CommandLog ring buffer** (`cvs/commandlog.go`) — every invocation is
  recorded with command, args, stdout, stderr, exit code, duration. The
  TUI's Console panel renders this buffer.
- **Unread-error counter** — failures increment a counter that surfaces
  in the tab bar as `⚠ N`. Focusing the console resets it.

This is the only path from lazycvs to the cvs binary. No code calls
`exec.Command` directly for `cvs`.

## App: the top-level Bubble Tea model

`App` (in `tui/app_core.go`) is the only model passed to
`tea.NewProgram`. Sub-models (`TreeModel`, `FileListModel`, etc.) are
embedded as fields and receive a slice of `App.Update`'s message stream
when their tab is active.

Key fields:

```go
type App struct {
    activeTab  int                // 0=Tree 1=Fav 2=Staged 3=History
    focus      Panel              // PanelLeft / PanelRight / PanelConsole

    // Sub-models — each owns its own panel state.
    tree, filelist, favorites, staged, history, console, dialog, search ModelType

    // Single source of truth for CVS status. Owned by App, mutated only
    // inside Update() (which Bubble Tea serializes on the main goroutine).
    statusMap   map[string]string  // path → "M" / "C" / "?" / ...
    baseRevMap  map[string]string  // path → working revision (sticky-aware)

    // Per-file History caches. Persist across file switches; only
    // invalidated when an action mutates the file.
    histRevisions map[string]*cvs.FileHistory          // path → revisions
    histDiffs     map[string]map[string]*cvs.DiffResult // path → "from:to" → diff
    histContents  map[string]map[string]string         // path → rev → content
    histPending   map[string]bool                      // dedup in-flight loads

    exec        *cvs.CVSExecutor
    cmdLog      *cvs.CommandLog
    cfgMgr      *config.ConfigManager

    // Epoch-based staleness tracking for status refreshes — late
    // results from older epochs get dropped at the handler.
    statusEpoch uint64
    dirEpoch    map[string]uint64
}
```

`statusMap` is the **single source of truth** for per-file status.
Tree, FileList, Staged, and History all derive their displays from it.
Anything that needs to know "what is this file's status?" calls
`m.resolveFileStatus(path)` which checks `statusMap` first, then falls
back to walking ancestor `CVS/` markers.

## Async caching pattern (path-tagged messages)

Every cvs invocation that touches data the UI cares about follows the
same pattern:

```
1. tea.Cmd dispatches `cvs <op>` in a goroutine
2. Result wrapped in a Msg type that carries the path it was for
3. Update() handler stores the result into a path-keyed cache
4. If the user is currently viewing that path, the model is also
   updated and a re-render happens
5. Otherwise the result sits in the cache for next time
```

The path-tagging matters: results can arrive after the user has
switched files. Without the path tag, a slow result from file A could
poison file B's view. With it, results land in their source path's
cache slot regardless of what's on screen.

Example for the History tab:

```
User opens History on foo.go      tea.Cmd: loadHistory("foo.go")
                                    │
                                    ▼  (background)
                               cvs log foo.go
                                    │
                                    ▼
User switches to bar.go        historyLoadedMsg{path:"foo.go", ...}
                                    │
                                    ▼ Update()
                          App.histRevisions["foo.go"] = result
                          (Apply to model only if Path() == "foo.go")

User switches back to foo.go    openHistoryFor("foo.go")
                                    │
                                    ▼ Cache hit!
                          ApplyRevisions immediately, no cvs call
```

This is implemented across:
- `tui/history.go` — message types, async commands (`loadHistory`,
  `loadRevisionDiff`, `loadWorkingDiff`, `loadRevisionContent`,
  `loadBlame`)
- `tui/app_core.go` — `historyLoadedMsg` / `historyContentMsg` /
  `historyDiffMsg` handlers that write to caches
- `tui/app_history.go` — `serveContent` / `serveDiff` / `serveBlame`
  that check cache before dispatching
- `tui/app_status.go` — `loadDirStatus` / `dirStatusMsg` for `cvs status`

## Status refresh flow

```
User commits a file  →  commitDoneMsg
                            │
                            ├─→ refreshStatusForPaths([files])
                            │      │
                            │      └─→ tea.Batch of loadDirStatus per dir
                            │              │
                            │              └─→ cvs status -l <dir>
                            │                      │
                            │                      ▼
                            │                 dirStatusMsg
                            │                      │
                            │                      ▼ Update()
                            │            applyStatuses(toClear, statuses)
                            │                      │
                            │                      ├─ delete old statusMap entries
                            │                      ├─ merge new ones (M/C/?/U/...)
                            │                      ├─ thread WorkingRev → baseRevMap
                            │                      ├─ tree.RefreshStatus()
                            │                      └─ staged.Refresh()
                            │
                            └─→ invalidateHistoryCache([files])
                                   │
                                   ├─ drop hist* entries for each path
                                   └─ if currently viewing one: reload
```

Targeted refresh is much cheaper than `cvs -n update` on the whole
working copy. After every action, only the affected directories rescan.

## Per-file caches in History

The History tab caches three things per file at the App level (not at
the model — App is where caches that should survive switches live):

```go
histRevisions map[string]*cvs.FileHistory
histDiffs     map[string]map[string]*cvs.DiffResult  // path → "from:to" → diff
histContents  map[string]map[string]string           // path → rev → content
```

Cache key construction is centralized in `tui/app_history.go`:

```go
pendingLog(path)                    → "log:<path>"
pendingContent(path, rev)           → "content:<path>:<rev>"
pendingDiff(path, from, to)         → "diff:<path>:<from>:<to>"
diffCacheKey(from, to)              → "<from>:<to>"
const blameRev   = "@blame"
const workingRev = "@working"
```

`workingRev` is always on the **right** side of a diff pair — `cvs diff`
only reports `rev → working`, so the cache key normalization avoids
duplicate entries for the same logical pair.

## HistoryModel cursor space

The History tab's left panel shows real CVS revisions, plus optionally
a synthetic "working copy" pseudo-row at virtual index 0. To keep the
data layer (`m.revisions`) clean — it's just the `cvs log` output — the
pseudo-row is added through index translation, not by injecting a fake
Revision struct:

```go
func (m HistoryModel) numRows() int {
    if m.hasWorkingRow {
        return len(m.revisions) + 1
    }
    return len(m.revisions)
}

func (m HistoryModel) revisionIndex(row int) int {
    if m.hasWorkingRow {
        if row == 0 {
            return -1   // pseudo-row
        }
        return row - 1
    }
    return row
}

func (m HistoryModel) IsWorkingCopyRow() bool { ... }
func (m HistoryModel) BaseRevision() *cvs.Revision { ... }
func (m HistoryModel) HasCompare() bool { ... }
func (m HistoryModel) CompareAnchorRev() *cvs.Revision { ... }
```

All cursor / anchor math goes through these helpers; no callsite
manually adjusts indexes. `BaseRevision` is sticky-tag aware: it
returns the row whose `Number == m.baseRev` (the working revision from
`cvs status`), falling back to `revisions[0]` only when unknown.

## Resize / sizing

Every sub-model implements:

```go
SetSize(d PanelDims)
```

where `PanelDims` is a struct with `LeftW / RightW / Height int`. The
`updateSizes()` in `app_layout.go` calls `SetSize` on every sub-model
with named fields:

```go
m.tree.SetSize(PanelDims{LeftW: innerLeftW, Height: contentH})
m.history.SetSize(PanelDims{LeftW: innerLeftW, RightW: innerRightW, Height: contentH})
m.console.SetSize(PanelDims{LeftW: m.width - 2, Height: consoleH})
```

Each model reads only the fields it cares about. A two-panel model
(staged, history) reads both `LeftW` and `RightW`; a single-panel
model (tree, filelist, favorites, console, dialog) reads one. Unused
fields are zero-valued and silently ignored — no sentinel logic.

## Encoding

CVS predates the UTF-8 default and emits whatever bytes the source
files contain. Latin-1 ("ü" = `0xFC`) is invalid UTF-8, which makes
lipgloss's width calculation miscount cells, which makes the layout
overflow the panel, which makes the terminal wrap the line, which
shifts subsequent rows.

`cvs.EnsureUTF8(s)` (`cvs/encoding.go`) fast-paths via
`utf8.ValidString` and falls back to a byte-to-rune transcoding when
invalid. Latin-1 maps 1:1 onto U+0000–U+00FF, so the conversion is
lossless.

Applied at every loader that pipes file content to the renderer:
- `loadRevisionContent`
- `loadRevisionDiff` / `loadWorkingDiff`
- `loadBlame`
- `loadPreviewDiff`
- File-preview overlay (`p` key on tree)

Status / log / annotate / update parsers don't need it — their tokens
are reliably ASCII.

## Key dispatch

Three layers, in priority order, each handled in `tui/app_keys.go`:

1. **Stage commit input** — when staged tab has focus on the right
   panel and the input is focused, almost every key goes through it.
   `Esc` blurs.
2. **Global keys (`handleKeyPriority` + `handleGlobalKey`)** — `q`,
   `Tab`, `Tab1-4`, `[` `]` `Ctrl-J`, `?`, `s`, `u`, `U`, `Esc`,
   `h`/`l` for collapse / focus, etc.
3. **Per-tab / per-panel delegate (`delegateKey`)** — file actions
   (`c`/`r`/`D`/`e`/`E`/`M`/`a`/`i`/`d`/`o`/`p`), navigation (`space`
   for marking, `A` for mark-all), and routing into the active model's
   `Update`.

The History tab has a small twist: mode-toggle keys
(`p` / `d` / `b` / `w` / `space` / `Enter`) work from either panel.
`delegateHistoryKey` is called from both the left and right panel
cases; only navigation (`j`/`k`/`PgUp`/`PgDn`) on the right panel falls
through to viewport scrolling.

## Configuration

`config/config.go` parses TOML via `BurntSushi/toml`. The
`ConfigManager` provides a single `Get()` snapshot. Keys are documented
in `USER_GUIDE.md`.

## Testing

- **CVS parsers** (`cvs/`) are pure functions and have table-driven
  unit tests. `go test ./cvs/...` should always pass.
- **End-to-end** uses `scripts/demo.py` (`make demo`) which generates a
  self-contained CVS working copy with M / C / A / R / ? statuses and
  multiple revisions. No server required.
- **TUI** has no automated tests; the demo + manual exploration is the
  current strategy. Adding bubbletea snapshot tests is feasible if the
  cost vs benefit ever shifts.

## Tech stack

- Go 1.22+, stdlib for filesystem and process plumbing
- [`charmbracelet/bubbletea`](https://github.com/charmbracelet/bubbletea) — TUI runtime
- [`charmbracelet/bubbles`](https://github.com/charmbracelet/bubbles) — viewport, textinput
- [`charmbracelet/lipgloss`](https://github.com/charmbracelet/lipgloss) + `x/ansi` — styling
- [`sahilm/fuzzy`](https://github.com/sahilm/fuzzy) — fuzzy file finder
- [`BurntSushi/toml`](https://github.com/BurntSushi/toml) — config parsing
- [`atotto/clipboard`](https://github.com/atotto/clipboard) — copy from console
- The system `cvs` binary (1.11+) — invoked via `os/exec`

## Issue notes

`issues/` collects post-mortems for non-trivial bugs. Each entry:
1. Problem (what the user observed)
2. Cause (what was actually wrong)
3. Fix (what changed)
4. Sometimes: lessons / wrong hypotheses tried first

Notable ones:
- **010** — History panel refactor (per-file caches, slim model)
- **011** — Latin-1 content causing render glitches (debugging journey)
- **007** — Ghost highlighted rows from ANSI state corruption
- **003 / 005 / 008 / 009** — the History scroll-rendering chain

These exist because the History panel had a long history of subtle
bugs that took several attempts to fully understand. Reading them in
order shows how the architecture in `app_history.go` ended up at its
current shape.

## Where things live (cheat sheet)

| If you're looking for...                       | Look in                |
|------------------------------------------------|------------------------|
| Add a new key binding                          | `tui/keys.go` + handler in `tui/app_keys.go` |
| Add a new tab                                  | `tui/app_core.go` (Tab const) + layout in `app_layout.go` |
| Change how a `cvs` command is parsed           | `cvs/<command>.go` + tests |
| Change how the file list is rendered           | `tui/filelist.go`      |
| Change History tab caching / loading           | `tui/app_history.go`   |
| Add a new status code                          | `tui/app_status.go` `cvsStatusCode` |
| Hook a new action after commit                 | `commitDoneMsg` handler in `tui/app_core.go` |
| Add a dialog                                   | `tui/dialog.go` (kind + Open/update/view triplet) |
| Configure external tools                       | `tui/external.go` + config schema in `config/` |

## Design choices worth knowing

1. **Per-path caches at App level, not in models.** Models are
   ephemeral views; data outlives them.
2. **`statusMap` is the single source of truth.** Other places (tree
   counts, file list status column, staging view) read from it; nothing
   maintains its own copy.
3. **Path-tagged async messages.** Every msg carries the path it was
   for, so cache writes are safe across file switches.
4. **Epoch-based staleness for status.** Refresh increments
   `statusEpoch`; the result handler ignores msgs whose epoch is older
   than the latest applied for that directory.
5. **One way to navigate.** `j/k`, `g/G`, `PgUp/PgDn` work in every
   scrollable list. No per-tab navigation lore.
6. **CVS predates UTF-8.** `cvs.EnsureUTF8` runs on every renderable
   cvs output; fast path for valid UTF-8, transcode for Latin-1.
7. **`cvs` is the only authority.** lazycvs never tries to predict CVS
   state from on-disk metadata; every status / log / diff is a real
   `cvs` call. The caching makes this cheap.
