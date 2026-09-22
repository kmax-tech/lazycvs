package cvs

import (
	"os"
	"path/filepath"
	"strings"
)

// Sticky is what a working-copy directory is pinned to, as recorded in
// its CVS/Tag file.
//
// CVS records stickiness per directory, not per checkout: `cvs update
// -r release-2` inside one subdirectory pins that subdirectory alone,
// and every command run there afterwards works against that branch
// while the rest of the tree stays on the trunk. Nothing in the file
// listing says so, which is how an afternoon's work ends up committed
// to the wrong branch.
type Sticky struct {
	// Name is the tag, branch or date. Empty means the trunk.
	Name string
	// Date is true when the stickiness is a date (`cvs update -D`)
	// rather than a tag or branch.
	Date bool
	// NonBranch is true for a sticky tag that is not a branch — a
	// checkout of it is not committable, which is worth knowing before
	// you start editing.
	NonBranch bool
}

// TrunkLabel is what CVS calls the trunk, and what the UI shows when a
// directory carries no sticky tag at all.
const TrunkLabel = "MAIN"

// Label is the short form for the header: the trunk's name, the tag,
// or a date marked as one.
func (s Sticky) Label() string {
	switch {
	case s.Name == "":
		return TrunkLabel
	case s.Date:
		return "@" + s.Name
	default:
		return s.Name
	}
}

// OnTrunk reports whether this directory follows the trunk.
func (s Sticky) OnTrunk() bool { return s.Name == "" }

// ReadSticky reads dir/CVS/Tag. A missing or unreadable file means the
// trunk — that is also what CVS itself concludes, so an unreadable file
// must not be reported as an error the UI then has to explain.
//
// The file is one line whose first byte is the kind: T for a branch
// sticky tag, N for a non-branch tag, D for a date. Anything else is
// treated as a plain name, because guessing wrong about the kind is
// better than showing nothing.
func ReadSticky(dir string) Sticky {
	data, err := os.ReadFile(filepath.Join(dir, "CVS", "Tag"))
	if err != nil {
		return Sticky{}
	}
	line := strings.TrimSpace(string(data))
	if line == "" {
		return Sticky{}
	}
	switch line[0] {
	case 'T':
		return Sticky{Name: strings.TrimSpace(line[1:])}
	case 'N':
		return Sticky{Name: strings.TrimSpace(line[1:]), NonBranch: true}
	case 'D':
		return Sticky{Name: strings.TrimSpace(line[1:]), Date: true}
	default:
		return Sticky{Name: line}
	}
}
