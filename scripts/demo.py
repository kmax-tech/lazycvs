#!/usr/bin/env python3
"""Build a self-contained CVS demo for testing lazycvs features.

Creates a local CVS repository (no network/server needed), checks out a
working copy, adds several commits to give a real history, and produces a real
merge conflict via two parallel checkouts. The result is a directory you can
cd into and launch lazycvs against to exercise every TUI feature: status
markers, history, diff, blame, compare, external diff/merge tools.

Usage:
    scripts/demo.py [destination-dir]    # default: ./lazycvs-demo

Re-running blows the destination away — safe for repeated tests.

stdlib only.
"""
from __future__ import annotations

import shutil
import subprocess
import sys
import tempfile
from pathlib import Path


def cvs(*args: str, cwd: Path | None = None, allow_failure: bool = False) -> None:
    """Run cvs with the given args. Raises on non-zero unless allow_failure."""
    cmd = ["cvs", *args]
    result = subprocess.run(cmd, cwd=cwd, text=True, capture_output=True)
    if result.returncode != 0 and not allow_failure:
        sys.stderr.write(f"\ncvs {' '.join(args)} failed (cwd={cwd}):\n")
        sys.stderr.write(result.stdout)
        sys.stderr.write(result.stderr)
        raise SystemExit(result.returncode)


def write(path: Path, content: str, *, binary: bytes | None = None) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    if binary is not None:
        path.write_bytes(binary)
    else:
        path.write_text(content)


def commit_change(wc: Path, rel: str, message: str, content: str) -> None:
    """Overwrite a tracked file in the working copy and commit it."""
    write(wc / rel, content)
    cvs("-Q", "commit", "-m", message, rel, cwd=wc)


def build_long_history(wc: Path, rel: str, n: int) -> None:
    """Hammer one file with N small commits so the History tab has enough
    revisions to make the streaming loader visibly batch results.
    Each commit changes a single line — the diff per rev is trivial,
    but the metadata (rev numbers, dates, authors, messages) is what
    we want to scroll through."""
    base = (wc / rel).read_text() if (wc / rel).exists() else "// version 0\n"
    for i in range(1, n + 1):
        content = base + f"\n// bump {i}\n"
        commit_change(wc, rel, f"bump version: rev {i}", content)
        base = content


def main() -> int:
    if shutil.which("cvs") is None:
        sys.stderr.write("error: cvs not found in PATH "
                         "(install via 'brew install cvs' / 'apt install cvs')\n")
        return 1

    dest = Path(sys.argv[1] if len(sys.argv) > 1 else "./_lazycvs-demo").resolve()
    repo = dest / "cvsroot"
    wc = dest / "wc"
    wc2 = dest / "wc-conflicting"

    print(f"==> wiping {dest} (if exists)")
    if dest.exists():
        shutil.rmtree(dest)
    dest.mkdir(parents=True)

    print(f"==> initializing local CVS repo at {repo}")
    cvs("-d", str(repo), "init")

    # Stage initial files in a temp dir for `cvs import`.
    with tempfile.TemporaryDirectory() as tmp:
        tmp_path = Path(tmp)

        write(tmp_path / "README.md", """\
# lazycvs demo project

Self-contained CVS working copy for exercising lazycvs.

Statuses you'll see in the file list:
  src/main.go            — clean (no local changes)
  src/util.go            — locally modified (M)
  src/version.go         — clean, 30+ revisions for streaming-load demo
  src/legacy_api.go      — clean, sticky-tagged STABLE_V1 @ 1.2
                            (HEAD is 1.4; the (working) badge sits on 1.2)
  src/scratch.go         — untracked (?)
  docs/CONTRIBUTING.md   — untracked (?)
  experiments/           — entire directory is new to CVS (?)
  src/feature.go         — added but uncommitted (A)
  docs/TODO.md           — added but uncommitted (A)
  docs/notes.txt         — IN CONFLICT (C) after parallel edit
  docs/notes-de.txt      — modified (M), Latin-1 encoded with umlauts
  docs/old-spec.md       — scheduled for removal (R)
  docs/icon.bin          — small binary file
  src/debug.log          — ignored by .cvsignore (I)
  docs/notes.aux         — ignored by .cvsignore (I)
  build/                 — ignored directory
  legacy/                — STALE: server-side repo entry was removed
  tests/blocker.txt      — local untracked, blocks server-side add
                            (triggers the move-away dialog on `u`)
""")

        write(tmp_path / "src/main.go", """\
package main

import "fmt"

func main() {
\tfmt.Println("Hello, demo!")
}
""")

        write(tmp_path / "src/util.go", """\
package main

func greet(name string) string {
\treturn "Hello, " + name + "!"
}
""")

        write(tmp_path / "docs/notes.txt", """\
Project notes
=============

This is a demo of lazycvs features.

It has multiple files, multiple revisions, and a conflict that you
can resolve from inside the TUI to test the merge tool integration.

Architecture:
- single-package Go program
- no external dependencies
- runs locally without a server
""")

        # Tiny binary blob — exercises the binary detector.
        write(
            tmp_path / "docs/icon.bin",
            content="",
            binary=b"\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01"
                   b"\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89",
        )

        print("==> importing initial revision")
        cvs("-d", str(repo), "-Q", "import", "-m", "initial import",
            "demo", "VENDOR", "INITIAL", cwd=tmp_path)

    print(f"==> checking out primary working copy at {wc}")
    cvs("-d", str(repo), "-Q", "checkout", "-d", str(wc), "demo")
    # We deliberately don't run `cvs admin -kb` on icon.bin. It would mark the
    # repo-side as binary but leave the working copy in text mode, which makes
    # subsequent `cvs update -n` report U for the file — confusing noise in
    # the demo. lazycvs's IsBinary still classifies the file as binary via the
    # NUL-byte sniff (see fs/scanner.go), which is what we actually want to
    # demonstrate here.

    # === History ===
    print("==> building history on src/main.go")
    commit_change(wc, "src/main.go", "expand greeting", """\
package main

import "fmt"

func main() {
\tfmt.Println("Hello, demo world!")
}
""")
    commit_change(wc, "src/main.go", "add helper function", """\
package main

import "fmt"

func main() {
\tfmt.Println("Hello, demo world!")
\thelper()
}

func helper() {
\tfmt.Println("(this is a helper)")
}
""")
    commit_change(wc, "src/main.go", "fix helper output", """\
package main

import "fmt"

func main() {
\tfmt.Println("Hello, demo world!")
\thelper()
}

func helper() {
\tfmt.Println("[helper] starting")
}
""")

    print("==> building history on src/util.go")
    commit_change(wc, "src/util.go", "make greeting friendlier", """\
package main

func greet(name string) string {
\treturn "Hi, " + name + "!"
}
""")
    commit_change(wc, "src/util.go", "add formal greeting", """\
package main

func greet(name string) string {
\treturn "Hi, " + name + "!"
}

func formalGreet(name string) string {
\treturn "Good day, " + name + "."
}
""")

    print("==> building history on docs/notes.txt")
    commit_change(wc, "docs/notes.txt", "extend notes", """\
Project notes
=============

This is a demo of lazycvs features.

It has multiple files, multiple revisions, and a conflict that you
can resolve from inside the TUI to test the merge tool integration.

Architecture:
- single-package Go program
- no external dependencies
- runs locally without a server

Status:
- All commits made through cvs CLI directly.
- The CVS repo is purely local (:local: pseudo-protocol).
""")

    # === Local-only modifications visible in the TUI ===
    print("==> introducing local modifications (M status) on src/util.go")
    write(wc / "src/util.go", """\
package main

func greet(name string) string {
\treturn "Hello there, " + name + "!"
}

func formalGreet(name string) string {
\treturn "Good day, " + name + "."
}
""")

    print("==> introducing untracked files (? status)")
    write(wc / "src/scratch.go", """\
package main

// scratch — local notes, not yet added to CVS
func scratch() {
\t// TODO: figure this out
}
""")
    write(wc / "docs/CONTRIBUTING.md", """\
# Contributing

Brand-new file in the demo working copy. Press `a` on it inside the TUI
to schedule it for addition (status flips ? → A), then `c` to commit.

## Workflow

1. Fork the demo
2. Make changes
3. Run the test suite
4. Open a pull request
""")

    print("==> introducing untracked directory with files (? dir)")
    write(wc / "experiments/prototype.go", """\
package experiments

func prototype() string {
\treturn "this whole directory is new to CVS"
}
""")
    write(wc / "experiments/config.yaml", """\
name: prototype
version: 0.1
enabled: true
""")

    print("==> adding .cvsignore and ignored files")
    write(wc / ".cvsignore", """\
*.log
*.tmp
*.aux
*.blg
build/
""")
    write(wc / "build/output.bin", content="", binary=b"\x00compiled\x00")
    write(wc / "src/debug.log", "2026-04-28 10:00:00 DEBUG starting up\n")
    write(wc / "docs/notes.aux", "\\relax\n")

    print("==> adding new files (A status — added, not committed)")
    write(wc / "src/feature.go", """\
package main

func newFeature() string {
\treturn "this feature is brand new"
}
""")
    write(wc / "docs/TODO.md", """\
# TODO

- [ ] Resolve the conflict in docs/notes.txt
- [ ] Commit the new feature.go
- [ ] Decide whether scratch.go should be tracked
""")
    cvs("-Q", "add", "src/feature.go", "docs/TODO.md", cwd=wc)

    # === Conflict scenario ===
    # Parallel WC commits to docs/notes.txt; primary WC has local edits on
    # the same line. After `cvs update` the primary WC's notes.txt has
    # `<<<<<<<` markers AND a `.#notes.txt.<rev>` backup of the pre-merge
    # local file — exactly what the M (3-way merge) keybinding needs.
    print("==> creating parallel working copy to provoke a conflict")
    cvs("-d", str(repo), "-Q", "checkout", "-d", str(wc2), "demo")
    write(wc2 / "docs/notes.txt", """\
Project notes
=============

This is a demo of lazycvs features.

It has multiple files, multiple revisions, and a conflict that you
can resolve from inside the TUI to test the merge tool integration.

Architecture (UPSTREAM EDIT):
- single-package Go program
- no external dependencies

Status:
- All commits made through cvs CLI directly.
- The CVS repo is purely local (:local: pseudo-protocol).
""")
    cvs("-Q", "commit", "-m", "stricter dependency rule",
        "docs/notes.txt", cwd=wc2)

    print("==> editing same line in primary WC, then 'cvs update' for conflict")
    write(wc / "docs/notes.txt", """\
Project notes
=============

This is a demo of lazycvs features.

It has multiple files, multiple revisions, and a conflict that you
can resolve from inside the TUI to test the merge tool integration.

Architecture (LOCAL EDIT):
- single-package Go program
- no external dependencies
- runs locally without a server
- prefer Go stdlib for everything

Status:
- All commits made through cvs CLI directly.
- The CVS repo is purely local (:local: pseudo-protocol).
""")
    # `cvs update` exits non-zero when it detects a conflict — that's fine.
    cvs("update", "docs/notes.txt", cwd=wc, allow_failure=True)

    # Drop the second WC; the conflict is now baked into the primary WC.
    shutil.rmtree(wc2)

    # === Long history → exercises the streaming history loader ===
    print("==> hammering src/version.go with 30 commits for streaming demo")
    write(wc / "src/version.go", "package main\n\nconst Version = 0\n")
    cvs("-Q", "add", "src/version.go", cwd=wc)
    cvs("-Q", "commit", "-m", "introduce version constant", "src/version.go", cwd=wc)
    build_long_history(wc, "src/version.go", 30)

    # === Latin-1 encoded file → exercises cvs.EnsureUTF8 ===
    # Keep this string ASCII + Latin-1 supplement only (umlauts, ß, accented
    # vowels). No em-dashes, arrows, quotes — they're outside Latin-1 and
    # the .encode("latin-1") below will raise UnicodeEncodeError.
    print("==> writing Latin-1 encoded file (docs/notes-de.txt)")
    latin1_text = (
        "Projekt-Notizen\n"
        "===============\n"
        "\n"
        "Das ist eine Datei mit deutschen Umlauten: ÄÖÜäöüß.\n"
        "Sie ist in Latin-1 kodiert, nicht UTF-8 -- lazycvs sollte\n"
        "das automatisch erkennen und für die Anzeige transcodieren.\n"
        "\n"
        "Größe: ungefähr 200 Bytes.\n"
        "Übersetzungen: für Deutsch / für Französisch / für Spanisch.\n"
    )
    write(wc / "docs/notes-de.txt", content="", binary=latin1_text.encode("latin-1"))
    cvs("-Q", "add", "docs/notes-de.txt", cwd=wc)
    cvs("-Q", "commit", "-m", "add German notes (Latin-1 encoded)",
        "docs/notes-de.txt", cwd=wc)
    # Modify it locally so the History tab's working-copy diff has something
    # interesting to render with the Latin-1 chars in it.
    modified_latin1 = latin1_text + (
        "\n"
        "Nachtrag (lokale Änderung):\n"
        "  * Schöne neue Überschrift hinzugefügt.\n"
    )
    write(wc / "docs/notes-de.txt", content="",
          binary=modified_latin1.encode("latin-1"))

    # === Sticky-tag file → working rev != HEAD, badge lands on the tagged rev ===
    # Use a fresh file so the tag operations don't have to fight with local
    # modifications. Build a small linear history, tag rev 1.2, keep
    # committing past it, then `cvs update -r TAG` to pin the working copy
    # at 1.2. The file stays clean (matches the sticky rev), so lazycvs's
    # workingMatchesBase logic places the `(working)` badge on the 1.2
    # row — not on HEAD (1.4). Demonstrates that BaseRevision is
    # sticky-tag aware.
    print("==> creating src/legacy_api.go pinned at STABLE_V1 (1.2 < HEAD)")
    write(wc / "src/legacy_api.go",
          "package main\n\n// v1 API\nfunc Legacy() string { return \"v1\" }\n")
    cvs("-Q", "add", "src/legacy_api.go", cwd=wc)
    cvs("-Q", "commit", "-m", "introduce legacy API v1",
        "src/legacy_api.go", cwd=wc)  # → 1.1
    commit_change(wc, "src/legacy_api.go", "promote API to v2",
                  "package main\n\n// v2 API\nfunc Legacy() string { return \"v2\" }\n")  # → 1.2
    cvs("-Q", "tag", "STABLE_V1", "src/legacy_api.go", cwd=wc)  # tag now at 1.2
    commit_change(wc, "src/legacy_api.go", "promote API to v3",
                  "package main\n\n// v3 API\nfunc Legacy() string { return \"v3\" }\n")  # → 1.3
    commit_change(wc, "src/legacy_api.go", "promote API to v4",
                  "package main\n\n// v4 API\nfunc Legacy() string { return \"v4\" }\n")  # → 1.4
    cvs("-Q", "update", "-r", "STABLE_V1", "src/legacy_api.go", cwd=wc)
    # File now contains v2 content, sticky tag = STABLE_V1, working rev = 1.2.

    # === R status → file scheduled for removal ===
    print("==> scheduling docs/old-spec.md for removal (R status)")
    write(wc / "docs/old-spec.md", "# Old spec\n\nObsolete — pending removal.\n")
    cvs("-Q", "add", "docs/old-spec.md", cwd=wc)
    cvs("-Q", "commit", "-m", "add doomed file", "docs/old-spec.md", cwd=wc)
    (wc / "docs/old-spec.md").unlink()
    cvs("-Q", "remove", "docs/old-spec.md", cwd=wc)
    # Don't commit the removal — leaves it in R status for the demo.

    # === Stale directory → cvs update reports "skipping directory" ===
    # Add a directory + file to the repo, then nuke its repo-side RCS files
    # so the working copy still has the CVS/ marker but cvs can't find the
    # repo end. cvs update -n then emits "cannot open directory" which the
    # parser turns into a stale-dir badge in the tree.
    print("==> creating a stale directory (legacy/) by removing its repo entry")
    write(wc / "legacy/leftover.txt", "Old module that was removed server-side.\n")
    cvs("-Q", "add", "legacy", cwd=wc)
    cvs("-Q", "add", "legacy/leftover.txt", cwd=wc)
    cvs("-Q", "commit", "-m", "add legacy module", cwd=wc)
    legacy_repo_dir = repo / "demo" / "legacy"
    if legacy_repo_dir.exists():
        shutil.rmtree(legacy_repo_dir)
    # Working copy still has wc/legacy/CVS/ pointing at the now-missing
    # repo dir → next `cvs update -n` reports it as stale.

    # === Move-away blocker → cvs update can't pull a server file in ===
    # Spin a parallel WC, add a NEW file there, commit. Then in the primary
    # WC, create a local untracked file with the same path. When the user
    # presses `u` in lazycvs the move-away dialog should pop up.
    print("==> staging a move-away blocker (tests/blocker.txt)")
    cvs("-d", str(repo), "-Q", "checkout", "-d", str(wc2), "demo")
    write(wc2 / "tests/blocker.txt",
          "Server side: this file was added by a colleague.\n")
    cvs("-Q", "add", "tests", cwd=wc2)
    cvs("-Q", "add", "tests/blocker.txt", cwd=wc2)
    cvs("-Q", "commit", "-m", "add tests/blocker.txt", cwd=wc2)
    shutil.rmtree(wc2)
    # Local-side: a totally different file with the same name.
    write(wc / "tests/blocker.txt",
          "Local: my own scratch file, never been in CVS.\n")

    # === Final summary ===
    status = subprocess.run(
        ["cvs", "-n", "-q", "update"],
        cwd=wc, text=True, capture_output=True,
    )

    lazycvs_path = Path(__file__).resolve().parent.parent / "lazycvs"

    print("\n" + "─" * 68)
    print(f"✓ Demo ready: {wc}\n")
    print("Status overview (from `cvs -n -q update`):")
    print(status.stdout or "  (clean — unexpected, did the script fail?)")
    print("Run lazycvs against it:\n")
    print(f"  cd {wc}")
    print(f"  {lazycvs_path}\n")
    print("Scenarios to exercise:\n")
    print("  Tree tab (1)")
    print("    - status markers: M, ?, A, C, R across the file list")
    print("    - experiments/ is a new directory: files inside show ?,")
    print("      committable (auto-adds parent dir + file)")
    print("    - tests/blocker.txt is untracked locally and SAME NAME as a")
    print("      file added on the (simulated) server side")
    print("    - press s to refresh — legacy/ should pick up the `! stale`")
    print("      badge after the dry-run update detects the missing repo dir")
    print("    - press A on a directory  → mark every changed file in subtree")
    print("    - press D on the marked set → bulk-remove dialog with listing")
    print("    - press i on the marked set → bulk-ignore (writes .cvsignore)\n")
    print("  History tab (4)")
    print("    - press d on src/version.go → 30+ revisions stream in batches")
    print("      (you should see the list grow rather than appear in one block)")
    print("    - press d on src/util.go    → file is dirty (M); the top row")
    print("      'working' (local changes) is the pseudo working-copy row,")
    print("      cursor on it shows working↔HEAD diff")
    print("    - press d on src/legacy_api.go → clean file pinned at sticky")
    print("      tag STABLE_V1 (rev 1.2). HEAD is 1.4, but the `(working)`")
    print("      badge correctly lands on 1.2 — sticky-tag aware.")
    print("    - press d on src/main.go    → clean file at HEAD; `(working)`")
    print("      badge sits on the latest revision (1.4)")
    print("    - press d on docs/notes-de.txt → Latin-1 file, dirty;")
    print("      the diff in the right pane shows umlauts cleanly thanks to")
    print("      cvs.EnsureUTF8")
    print("    - press space on a revision then move cursor → compare anchor")
    print("    - press space on the 'working' pseudo-row → anchor working")
    print("    - press w on a rev          → toggle vs-working")
    print("    - press p in diff mode      → side-by-side")
    print("    - press b                   → blame view")
    print("    - press </>                 → horizontal scroll\n")
    print("  Update + move-away resolution")
    print("    - press u on tests/blocker.txt (or in tests/) → cvs update will")
    print("      report the local file as 'in the way'. The DialogInTheWay")
    print("      pops up: m=move-aside / d=delete / k=keep")
    print("    - move-aside leaves tests/blocker.txt.moved-by-lazycvs next to")
    print("      the now-pulled server version\n")
    print("  Conflict resolution")
    print("    - on docs/notes.txt (C status), press M → external 3-way merge")
    print("      (configure [editor] merge_tool, e.g. \"meld\")")
    print("    - the .#notes.txt.<rev> backup is the pre-merge LOCAL side\n")
    print("  Staged tab (3)")
    print("    - mark several files with space, switch via 3")
    print("    - press c → commit input. Type a one-liner + Enter, OR")
    print("    - press Ctrl-E → opens $EDITOR for a multi-line message")
    print("      (lines starting with # are stripped)\n")
    print("Config (~/.config/lazycvs/config.toml):\n")
    print("  [editor]")
    print("  diff_tool  = \"meld\"     # or vscode, opendiff, kdiff3, …")
    print("  merge_tool = \"meld\"\n")
    print(f"To reset:\n  {sys.argv[0]} {dest}")
    print("─" * 68)
    return 0


if __name__ == "__main__":
    sys.exit(main())
