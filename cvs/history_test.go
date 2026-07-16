package cvs

import (
	"os"
	"path/filepath"
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

func TestMergeHistory(t *testing.T) {
	now := time.Now()
	ev := func(file, rev string, age time.Duration) HistoryEvent {
		return HistoryEvent{Code: "M", File: file, Rev: rev, RepoDir: "mod", Time: now.Add(-age)}
	}
	old := []HistoryEvent{
		ev("a.txt", "1.2", 2*time.Hour),
		ev("stale.txt", "1.1", 200*time.Hour), // beyond cutoff
	}
	fresh := []HistoryEvent{
		ev("b.txt", "1.5", 10*time.Minute),
		ev("a.txt", "1.2", 2*time.Hour), // overlap duplicate
	}

	got := MergeHistory(old, fresh, now.Add(-168*time.Hour))
	if len(got) != 2 {
		t.Fatalf("merged %d events, want 2 (dedup + cutoff): %+v", len(got), got)
	}
	if got[0].File != "b.txt" || got[1].File != "a.txt" {
		t.Errorf("not sorted newest-first: %+v", got)
	}

	// Full fetch: old == nil behaves as plain sort+cutoff.
	got = MergeHistory(nil, fresh, now.Add(-168*time.Hour))
	if len(got) != 2 {
		t.Errorf("nil-old merge: %d events, want 2", len(got))
	}
}

func TestHistoryStoreRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	now := time.Now().Round(time.Second)
	s := &HistoryStore{
		CVSRoot:       ":ext:user@host:/srv/cvsroot",
		LastFetch:     now,
		CoverageStart: now.AddDate(0, 0, -7),
		Events: []HistoryEvent{
			{Code: "M", Time: now, User: "anna", Rev: "1.2", File: "f.txt", RepoDir: "mod"},
		},
	}
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}

	got, err := LoadHistoryStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.CVSRoot != s.CVSRoot || !got.LastFetch.Equal(s.LastFetch) ||
		!got.CoverageStart.Equal(s.CoverageStart) || len(got.Events) != 1 {
		t.Errorf("roundtrip mismatch: %+v", got)
	}
	if got.Events[0].File != "f.txt" || got.Events[0].Code != "M" {
		t.Errorf("event mangled: %+v", got.Events[0])
	}

	// No half-written tmp file may survive the atomic save.
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Error("tmp file left behind by Save")
	}
}
