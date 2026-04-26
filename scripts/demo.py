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

Files in here cover every status the TUI cares about:
- src/main.go            — clean (no local changes)
- src/util.go            — locally modified (M)
- src/scratch.go         — untracked, needs `a` to add (?)
- docs/CONTRIBUTING.md   — untracked, needs `a` to add (?)
- src/feature.go         — added but uncommitted, needs `c` (A)
- docs/TODO.md           — added but uncommitted, needs `c` (A)
- docs/notes.txt         — IN CONFLICT (C) after parallel edit
- docs/icon.bin          — small binary file

The history of src/main.go and src/util.go has multiple revisions
so the History tab has something to scroll through.
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
- runs locally without a server
- vendored libraries are forbidden

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
    print("Things to test:\n")
    print("  Tree tab (1)")
    print("    - status markers: M (util.go), ? (scratch.go), A (feature.go,")
    print("      TODO.md), C (notes.txt), per-dir aggregate counts")
    print("    - press d on src/util.go    → History tab opens at WORK row")
    print("    - press space on a file     → mark for commit")
    print("    - press 3                   → Staged tab with marked files\n")
    print("  History tab (4)")
    print("    - top row \"WORK\" for files with M/C status")
    print("    - press d on a real revision → diff vs parent")
    print("    - press space on rev A, then space on rev B → free compare")
    print("    - press s in compare/diff   → side-by-side")
    print("    - press </>                 → horizontal scroll")
    print("    - press w on a rev          → compare rev vs working")
    print("    - press E                   → external diff tool\n")
    print("  Conflict resolution")
    print("    - on docs/notes.txt (C status), press M → external 3-way merge")
    print("      tool (configure [editor] merge_tool first, e.g. \"meld\")")
    print("    - the .#notes.txt.<rev> backup is the pre-merge LOCAL side\n")
    print("Config (~/.config/lazycvs/config.toml):\n")
    print("  [editor]")
    print("  diff_tool  = \"meld\"     # or vscode, opendiff, kdiff3, …")
    print("  merge_tool = \"meld\"\n")
    print(f"To reset (re-run with conflict back):\n  {sys.argv[0]} {dest}")
    print("─" * 68)
    return 0


if __name__ == "__main__":
    sys.exit(main())
