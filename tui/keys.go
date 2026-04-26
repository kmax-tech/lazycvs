package tui

import "github.com/charmbracelet/bubbles/key"

type keyMap struct {
	// Global
	Quit    key.Binding
	Tab     key.Binding
	BackTab key.Binding
	Tab1    key.Binding
	Tab2    key.Binding
	Tab3    key.Binding
	Tab4    key.Binding
	FocusL  key.Binding
	FocusR  key.Binding
	FocusC  key.Binding
	Update  key.Binding
	Help    key.Binding

	// Navigation
	Up      key.Binding
	Down    key.Binding
	Left    key.Binding
	Right   key.Binding
	Top     key.Binding
	Bottom  key.Binding
	Enter   key.Binding
	Space   key.Binding

	// File actions
	Diff    key.Binding
	Commit  key.Binding
	Revert  key.Binding
	Remove  key.Binding
	Edit    key.Binding
	EditDiff key.Binding
	Merge    key.Binding
	Open    key.Binding
	Add     key.Binding
	Ignore  key.Binding
	Search  key.Binding
	Filter key.Binding
	FavAdd  key.Binding
	FavDel  key.Binding

	// Modes
	Blame          key.Binding
	SideBySide     key.Binding
	CompareWorking key.Binding
	ViewMode       key.Binding

	// Console
	Copy  key.Binding
	Clear key.Binding

	// Dialog
	Escape key.Binding
	Yes    key.Binding
	No     key.Binding
}

var keys = keyMap{
	Quit:    key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "quit")),
	Tab:     key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "next tab")),
	BackTab: key.NewBinding(key.WithKeys("shift+tab"), key.WithHelp("S-tab", "prev tab")),
	Tab1:    key.NewBinding(key.WithKeys("1"), key.WithHelp("1", "tree")),
	Tab2:    key.NewBinding(key.WithKeys("2"), key.WithHelp("2", "favorites")),
	Tab3:    key.NewBinding(key.WithKeys("3"), key.WithHelp("3", "staged")),
	Tab4:    key.NewBinding(key.WithKeys("4"), key.WithHelp("4", "history")),
	FocusL:  key.NewBinding(key.WithKeys("["), key.WithHelp("[", "left panel")),
	FocusR:  key.NewBinding(key.WithKeys("]"), key.WithHelp("]", "right panel")),
	FocusC:  key.NewBinding(key.WithKeys("ctrl+j"), key.WithHelp("C-j", "console")),
	Update:  key.NewBinding(key.WithKeys("u"), key.WithHelp("u", "update")),
	Help:    key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),

	Up:     key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("k", "up")),
	Down:   key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("j", "down")),
	Left:   key.NewBinding(key.WithKeys("h"), key.WithHelp("h", "collapse")),
	Right:  key.NewBinding(key.WithKeys("l"), key.WithHelp("l", "expand")),
	Top:    key.NewBinding(key.WithKeys("g"), key.WithHelp("gg", "top")),
	Bottom: key.NewBinding(key.WithKeys("G"), key.WithHelp("G", "bottom")),
	Enter:  key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "select")),
	Space:  key.NewBinding(key.WithKeys(" "), key.WithHelp("space", "mark")),

	Diff:    key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "diff")),
	Commit:  key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "commit")),
	Revert:  key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "revert")),
	Remove:  key.NewBinding(key.WithKeys("D"), key.WithHelp("D", "remove")),
	Edit:    key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "edit")),
	EditDiff: key.NewBinding(key.WithKeys("E"), key.WithHelp("E", "diff tool")),
	Merge:   key.NewBinding(key.WithKeys("M"), key.WithHelp("M", "merge tool")),
	Open:    key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "open")),
	Add:     key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "add")),
	Ignore:  key.NewBinding(key.WithKeys("i"), key.WithHelp("i", "ignore")),
	Search:  key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "search")),
	Filter: key.NewBinding(key.WithKeys("F"), key.WithHelp("F", "filter")),
	FavAdd:  key.NewBinding(key.WithKeys("+"), key.WithHelp("+", "add fav")),
	FavDel:  key.NewBinding(key.WithKeys("-"), key.WithHelp("-", "del fav")),

	Blame:          key.NewBinding(key.WithKeys("b"), key.WithHelp("b", "blame")),
	SideBySide:     key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "side-by-side")),
	CompareWorking: key.NewBinding(key.WithKeys("w"), key.WithHelp("w", "vs working")),
	ViewMode:       key.NewBinding(key.WithKeys("v"), key.WithHelp("v", "view mode")),

	Copy:  key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "copy")),
	Clear: key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "clear")),

	Escape: key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	Yes:    key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "yes")),
	No:     key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "no")),
}

func keyHelp(bindings ...key.Binding) string {
	s := ""
	for i, b := range bindings {
		if i > 0 {
			s += "  "
		}
		s += keyStyle.Render(b.Help().Key) + ":" + b.Help().Desc
	}
	return s
}
