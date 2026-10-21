package tui

import "testing"

// The streaming parser must pick the -k mode out of the log header and
// must not mistake a commit message line of the same shape for it.
func TestRevisionParserKeywordSubstitution(t *testing.T) {
	lines := []string{
		"RCS file: /srv/cvsroot/mod/doc.pdf,v",
		"Working file: doc.pdf",
		"head: 1.2",
		"keyword substitution: b",
		"total revisions: 2;\tselected revisions: 2",
		"description:",
		"----------------------------",
		"revision 1.2",
		"date: 2024/01/02 10:00:00;  author: max;  state: Exp;  lines: +1 -1",
		"keyword substitution: kv",
		"----------------------------",
		"revision 1.1",
		"date: 2024/01/01 10:00:00;  author: max;  state: Exp;",
		"first",
		"=============================================================================",
	}
	p := newRevisionParser()
	var revs []string
	for _, l := range lines {
		if r := p.feed(l); r != nil {
			revs = append(revs, r.Number)
		}
	}
	if r := p.finish(); r != nil {
		revs = append(revs, r.Number)
	}
	if p.keywordSubstitution != "b" {
		t.Errorf("keywordSubstitution = %q, want %q", p.keywordSubstitution, "b")
	}
	if len(revs) != 2 || revs[0] != "1.2" || revs[1] != "1.1" {
		t.Errorf("revisions = %v, want [1.2 1.1]", revs)
	}
}
