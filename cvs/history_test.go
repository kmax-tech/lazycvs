package cvs

import (
	"testing"
	"time"
)

func TestParseHistory(t *testing.T) {
	output := `M 2026-07-08 10:11 +0000 anna 1.13 notes.txt webis/webis-applications-22 == <remote>
A 2026-07-08 09:02 +0000 ines 1.1 new-file.txt webis == ~/work/webis
R 2026-07-07 18:30 +0000 timo 1.4 old.txt webis/sub/deep
O 2026-07-08 08:00 +0000 anna webis =webis= ~/elsewhere
No records selected.
`
	events := ParseHistory(output)
	if len(events) != 3 {
		t.Fatalf("got %d events, want 3 (O record and notice skipped): %+v", len(events), events)
	}

	first := events[0]
	if first.Code != "M" || first.User != "anna" || first.Rev != "1.13" ||
		first.File != "notes.txt" || first.RepoDir != "webis/webis-applications-22" {
		t.Errorf("first event parsed wrong: %+v", first)
	}
	want := time.Date(2026, 7, 8, 10, 11, 0, 0, time.UTC)
	if !first.Time.Equal(want) {
		t.Errorf("first event time = %v, want %v", first.Time, want)
	}

	if events[2].Code != "R" || events[2].RepoDir != "webis/sub/deep" {
		t.Errorf("R record without '== workdir' suffix parsed wrong: %+v", events[2])
	}
}

func TestFilterHistoryByRepoPrefix(t *testing.T) {
	events := []HistoryEvent{
		{File: "a", RepoDir: "webis"},
		{File: "b", RepoDir: "webis/sub"},
		{File: "c", RepoDir: "webis/sub/deep"},
		{File: "d", RepoDir: "webis-other"}, // sibling with common string prefix
	}

	got := FilterHistoryByRepoPrefix(events, "webis/sub")
	if len(got) != 2 || got[0].File != "b" || got[1].File != "c" {
		t.Errorf("prefix webis/sub: got %+v, want b and c", got)
	}

	got = FilterHistoryByRepoPrefix(events, "webis")
	if len(got) != 3 {
		t.Errorf("prefix webis must NOT match webis-other: got %+v", got)
	}
}
