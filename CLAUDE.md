# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

**lazycvs** is a lightweight, cross-platform CVS client written in Go with a lazygit-inspired terminal UI. It wraps the `cvs` CLI for daily workflow: status dashboard, diffs, commits, conflict resolution, history viewing, and stale directory cleanup. All planning docs are in German.

## Build & Run Commands

```bash
make build                       # Build binary
make release                     # Cross-compile for linux/mac/windows
make demo                        # Build + create demo working copy + launch
go run . [path]                  # Open TUI on path (default: cwd)
go run . -cvs /path/to/cvs       # Override CVS binary
go run . -config /path/cfg.toml  # Override config path
go test ./...                    # Run all tests
go test ./cvs/...                # Run CVS parser tests only
go test -run TestParseDiff ./cvs/  # Run a single test
```

## Architecture

The app follows a strict **data/rendering separation**: the CVS layer produces Go structs, never strings. The TUI is the only renderer; it owns layout/style and reads the parsed structs read-only.

### Key Layers

- **`main.go`** — CLI flags, working-copy resolution (cwd → `default_path` fallback), CVS binary lookup, TUI launch
- **`cvs/`** — CVS data layer (pure functions: string in, struct out)
  - `executor.go` — Runs cvs commands via `os/exec` with mutex serialization and timeout
  - `parser.go` — Parses `cvs update` output into `UpdateResult` structs
  - `diff.go` — Unified diff parser, side-by-side builder
  - `conflict.go` — Conflict marker parser (`<<<<<<<` / `=======` / `>>>>>>>`), `HasConflictMarkers` precondition check
  - `log.go` / `annotate.go` / `status.go` — parsers for `cvs log`, `cvs annotate`, `cvs status`
  - `commandlog.go` — Ring buffer with unread-error counter
- **`tui/`** — Bubble Tea TUI (lazygit-style panels). See [tui_architecture.md](memory/tui_architecture.md) for the file split, message flow, and central commit/remove/update pattern.
- **`fs/`** — filesystem helpers (binary detection, local-vs-server compare)
- **`config/`** — TOML config via `BurntSushi/toml` (the only external dependency outside charmbracelet/*)
- **`scripts/demo.py`** — generates a self-contained CVS working copy with history + a real merge conflict for end-to-end testing

### CVS Executor

All CVS commands go through a single `CVSExecutor` with a mutex (one CVS command at a time), configurable timeout (default 60s), and automatic logging to the `CommandLog` ring buffer that feeds the TUI's Console panel.

## Tech Stack

- Go 1.22+, stdlib for filesystem and process plumbing (`os/exec`, `path/filepath`)
- [`charmbracelet/bubbletea`](https://github.com/charmbracelet/bubbletea) — TUI runtime
- [`charmbracelet/bubbles`](https://github.com/charmbracelet/bubbles) — viewport, textinput, key bindings
- [`charmbracelet/lipgloss`](https://github.com/charmbracelet/lipgloss) + `x/ansi` — styling, ANSI-aware truncation
- [`sahilm/fuzzy`](https://github.com/sahilm/fuzzy) — fuzzy file search
- [`BurntSushi/toml`](https://github.com/BurntSushi/toml) — config parsing
- The system `cvs` binary (1.11+) — invoked via `os/exec`

## Testing

CVS parsers are pure functions (string → struct) and should have table-driven unit tests. End-to-end testing uses `scripts/demo.py` (run via `make demo`), which creates a self-contained local CVS repository with every status the TUI cares about plus a real merge conflict — no server required.

## Language

Planning docs are in German, but all code, comments, commit messages, and identifiers must be in English.

## Path Validation

All handlers accepting file paths must call `ValidatePath()` which checks for path traversal and symlink escapes before any CVS operation.

## Issue Notes

When a bug is discovered and fixed during implementation, create a short technical note in `issues/` (e.g. `issues/002-short-name.md`). Document: what the problem was, what caused it, and how it was fixed. Use the next sequential number. This happens automatically as part of the fix — no separate request needed.
