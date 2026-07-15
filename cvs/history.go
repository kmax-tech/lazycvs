package cvs

import (
	"sort"
	"strings"
	"time"
)

// HistoryEvent is one commit-ish record from `cvs history` — who did
// what to which file, when. Only the A/M/R record types are parsed
// (added / committed / removed); checkout, tag, and update records are
// noise for a "what changed recently" view.
type HistoryEvent struct {
	Code    string    // "A" added, "M" committed, "R" removed
	Time    time.Time
	User    string
	Rev     string
	File    string
	RepoDir string // repository dir relative to CVSROOT (no trailing /)
}

// ParseHistory parses the output of `cvs history -x AMR -a [-D date]`.
// Record format (CVS 1.11+, one per line):
//
//	M 2026-07-08 10:11 +0000 anna 1.13 notes.txt webis/webis-app-22 == <remote>
//
// Fields: code, date, time, zone, user, revision, file, repo-dir,
// optionally "== workdir". Lines that don't match (headers, other
// record types, the "No records selected" notice) are skipped.
func ParseHistory(output string) []HistoryEvent {
	var events []HistoryEvent
	for _, line := range strings.Split(output, "\n") {
		f := strings.Fields(line)
		if len(f) < 8 {
			continue
		}
		code := f[0]
		if code != "A" && code != "M" && code != "R" {
			continue
		}
		ts, err := time.Parse("2006-01-02 15:04 -0700", f[1]+" "+f[2]+" "+f[3])
		if err != nil {
			continue
		}
		events = append(events, HistoryEvent{
			Code:    code,
			Time:    ts,
			User:    f[4],
			Rev:     f[5],
			File:    f[6],
			RepoDir: strings.TrimSuffix(f[7], "/"),
		})
	}
	return events
}

// MergeHistory folds a fresh (possibly overlapping) batch of events
// into an existing list: duplicates are dropped, events older than
// cutoff fall out, and the result comes back newest-first. This is
// what makes incremental refreshes work — `cvs history -D <last
// fetch - overlap>` ships only the new events, the overlap absorbs
// client/server clock skew, and the dedup here makes the overlap
// harmless. Pass old == nil for a full (initial) fetch.
func MergeHistory(old, fresh []HistoryEvent, cutoff time.Time) []HistoryEvent {
	// A revision number is unique per file, so RepoDir/File@Rev:Code
	// identifies an event without relying on timestamps.
	key := func(e HistoryEvent) string {
		return e.RepoDir + "/" + e.File + "@" + e.Rev + ":" + e.Code
	}
	seen := make(map[string]bool, len(old)+len(fresh))
	out := make([]HistoryEvent, 0, len(old)+len(fresh))
	for _, e := range append(append([]HistoryEvent{}, fresh...), old...) {
		if e.Time.Before(cutoff) || seen[key(e)] {
			continue
		}
		seen[key(e)] = true
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	return out
}

// FilterHistoryByRepoPrefix keeps events whose repo dir is prefix
// itself or lives below it. The history database is server-global —
// this is what scopes it to the directory the user selected.
func FilterHistoryByRepoPrefix(events []HistoryEvent, prefix string) []HistoryEvent {
	prefix = strings.TrimSuffix(prefix, "/")
	var out []HistoryEvent
	for _, e := range events {
		if e.RepoDir == prefix || strings.HasPrefix(e.RepoDir, prefix+"/") {
			out = append(out, e)
		}
	}
	return out
}
