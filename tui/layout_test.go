package tui

import "testing"

// The console's height is a pure function of three inputs, so the
// collapse/expand rules can be checked as a table instead of by
// watching a terminal. An auto-sizing panel whose rules live scattered
// across handlers is exactly the kind that starts flickering.
func TestConsoleHeightTransitions(t *testing.T) {
	ui := uiSizes{consoleHeight: 6}

	cases := []struct {
		name string
		st   consoleState
		want int
	}{
		{"idle and unfocused collapses", consoleState{}, consoleCollapsed},
		{"focus expands", consoleState{focused: true}, 6},
		{"an unseen error expands it unfocused",
			consoleState{unreadErrors: 1}, 6},
		{"a seen error collapses again",
			consoleState{unreadErrors: 0}, consoleCollapsed},
		{"the user's own height wins while focused",
			consoleState{focused: true, override: 12}, 12},
		{"the user's height does not keep it open",
			consoleState{override: 12}, consoleCollapsed},
		{"an override below one line is still a line",
			consoleState{focused: true, override: 0}, 6},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := consoleHeight(ui, c.st); got != c.want {
				t.Errorf("consoleHeight(%+v) = %d, want %d", c.st, got, c.want)
			}
		})
	}
}

// The sequence that matters in practice: a command fails while the user
// is in the file pane, the panel opens itself, and it stays open until
// the user has actually looked — which is the unread counter's job, not
// a second flag of our own.
func TestConsoleStaysOpenUntilTheErrorIsSeen(t *testing.T) {
	ui := uiSizes{consoleHeight: 6}

	afterFailure := consoleState{unreadErrors: 1}
	if got := consoleHeight(ui, afterFailure); got == consoleCollapsed {
		t.Fatal("a failure should open the console")
	}
	whileReading := consoleState{focused: true, unreadErrors: 1}
	if got := consoleHeight(ui, whileReading); got != 6 {
		t.Fatalf("console should stay open while read: %d", got)
	}
	// Focus clears the unread counter (App.Update), so afterwards:
	afterReading := consoleState{}
	if got := consoleHeight(ui, afterReading); got != consoleCollapsed {
		t.Fatalf("console should collapse once the error was seen: %d", got)
	}
}

// The tree used to be pinned between 25 and 40 columns whatever the
// terminal did, so a 200-column window gave the tree 40 and the file
// list 160.
func TestTreeWidth(t *testing.T) {
	cases := []struct {
		name  string
		ui    uiSizes
		total int
		want  int
	}{
		{"auto on a narrow terminal", uiSizes{}, 60, 25},
		{"auto on a normal terminal", uiSizes{}, 120, 40},
		{"auto stays clamped on a wide one", uiSizes{}, 240, 40},
		{"configured width is honoured", uiSizes{treeWidth: 70}, 240, 70},
		{"configured width never takes more than half", uiSizes{treeWidth: 200}, 120, 60},
		{"a silly small width still leaves a pane", uiSizes{treeWidth: 2}, 120, 15},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := treeWidth(c.ui, c.total); got != c.want {
				t.Errorf("treeWidth(%+v, %d) = %d, want %d", c.ui, c.total, got, c.want)
			}
		})
	}
}

// Nothing but focus, an unseen error and the user's own +/- may change
// the console's height. A status refresh or a banner must not: a panel
// that resizes while you read it is worse than one that is too small.
func TestConsoleHeightIgnoresUnrelatedState(t *testing.T) {
	ui := uiSizes{consoleHeight: 6}
	base := consoleState{focused: true}
	if got, want := consoleHeight(ui, base), 6; got != want {
		t.Fatalf("baseline = %d, want %d", got, want)
	}
	// Same inputs, same answer — the function has no other state to
	// react to, which is the point of it being a function.
	for i := 0; i < 5; i++ {
		if got := consoleHeight(ui, base); got != 6 {
			t.Fatalf("height changed without an input changing: %d", got)
		}
	}
}
