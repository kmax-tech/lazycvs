package cvs

import "testing"

func TestParseLog(t *testing.T) {
	input := `
RCS file: /srv/cvsroot/code-in-progress/lazycvs-test/history-test/evolving-file.txt,v
Working file: history-test/evolving-file.txt
head: 1.5
branch:
locks: strict
access list:
symbolic names:
	release-1-0: 1.5
keyword substitution: kv
total revisions: 5;	selected revisions: 5
description:
----------------------------
revision 1.5
date: 2024/01/05 10:00:00;  author: testuser;  state: Exp;  lines: +1 -0
prepare for release
----------------------------
revision 1.4
date: 2024/01/04 10:00:00;  author: testuser;  state: Exp;  lines: +1 -0
add final section
----------------------------
revision 1.3
date: 2024/01/03 10:00:00;  author: testuser;  state: Exp;  lines: +1 -1
improve wording in first paragraph
----------------------------
revision 1.2
date: 2024/01/02 10:00:00;  author: testuser;  state: Exp;  lines: +1 -0
add second paragraph
----------------------------
revision 1.1
date: 2024/01/01 10:00:00;  author: testuser;  state: Exp;
initial version
=============================================================================
`
	history := ParseLog(input)

	if len(history.Revisions) != 5 {
		t.Fatalf("expected 5 revisions, got %d", len(history.Revisions))
	}

	// Check first revision (most recent)
	rev := history.Revisions[0]
	if rev.Number != "1.5" {
		t.Errorf("first revision: got %s, want 1.5", rev.Number)
	}
	if rev.Author != "testuser" {
		t.Errorf("author: got %s, want testuser", rev.Author)
	}
	if rev.Message != "prepare for release" {
		t.Errorf("message: got %q, want %q", rev.Message, "prepare for release")
	}
	if rev.LinesAdded != 1 {
		t.Errorf("lines added: got %d, want 1", rev.LinesAdded)
	}

	// Check tags
	if len(rev.Tags) != 1 || rev.Tags[0] != "release-1-0" {
		t.Errorf("tags: got %v, want [release-1-0]", rev.Tags)
	}

	// Check PrevNumber chain
	if history.Revisions[0].PrevNumber != "1.4" {
		t.Errorf("1.5 prev: got %s, want 1.4", history.Revisions[0].PrevNumber)
	}
	if history.Revisions[4].PrevNumber != "" {
		t.Errorf("1.1 prev: got %s, want empty", history.Revisions[4].PrevNumber)
	}

	// Last revision has no lines added/removed
	lastRev := history.Revisions[4]
	if lastRev.Number != "1.1" {
		t.Errorf("last revision: got %s, want 1.1", lastRev.Number)
	}
	if lastRev.Message != "initial version" {
		t.Errorf("last message: got %q, want %q", lastRev.Message, "initial version")
	}
}
