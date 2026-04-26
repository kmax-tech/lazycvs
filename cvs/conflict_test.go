package cvs

import "testing"

func TestParseConflicts(t *testing.T) {
	content := `\section{Shared Section}
<<<<<<< shared-doc.tex
This line will be changed by User B.
=======
This line will be changed by User A.
>>>>>>> 1.2
This line stays the same.
Another shared line.
`
	cf := ParseConflicts(content, "shared-doc.tex")

	if len(cf.Regions) != 1 {
		t.Fatalf("expected 1 region, got %d", len(cf.Regions))
	}

	r := cf.Regions[0]
	if r.ID != 1 {
		t.Errorf("region ID: got %d, want 1", r.ID)
	}
	if len(r.LocalLines) != 1 || r.LocalLines[0] != "This line will be changed by User B." {
		t.Errorf("local lines: got %v", r.LocalLines)
	}
	if len(r.ServerLines) != 1 || r.ServerLines[0] != "This line will be changed by User A." {
		t.Errorf("server lines: got %v", r.ServerLines)
	}
}

func TestParseConflictsMultiple(t *testing.T) {
	content := `line 1
<<<<<<< file.tex
local A
=======
server A
>>>>>>> 1.3
line 2
<<<<<<< file.tex
local B
local B2
=======
server B
>>>>>>> 1.3
line 3
`
	cf := ParseConflicts(content, "file.tex")

	if len(cf.Regions) != 2 {
		t.Fatalf("expected 2 regions, got %d", len(cf.Regions))
	}

	if len(cf.Regions[0].LocalLines) != 1 {
		t.Errorf("region 1 local: got %d lines", len(cf.Regions[0].LocalLines))
	}
	if len(cf.Regions[1].LocalLines) != 2 {
		t.Errorf("region 2 local: got %d lines, want 2", len(cf.Regions[1].LocalLines))
	}
}

func TestResolveConflictLocal(t *testing.T) {
	content := "before\n<<<<<<< f\nlocal\n=======\nserver\n>>>>>>> 1.1\nafter"
	resolved := ResolveConflict(content, "local", 0)
	expected := "before\nlocal\nafter"
	if resolved != expected {
		t.Errorf("got %q, want %q", resolved, expected)
	}
}

func TestResolveConflictServer(t *testing.T) {
	content := "before\n<<<<<<< f\nlocal\n=======\nserver\n>>>>>>> 1.1\nafter"
	resolved := ResolveConflict(content, "server", 0)
	expected := "before\nserver\nafter"
	if resolved != expected {
		t.Errorf("got %q, want %q", resolved, expected)
	}
}
