package tui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/key"
)

// bindingID identifies a binding by what it actually binds, so a
// registry entry and the keyMap field it came from compare equal
// without pointer identity.
//
// The description is part of the identity because two bindings can be
// otherwise identical: keys.Preview and keys.SideBySide are both "p"
// with help key "p", separated only by what they claim to do. That
// they are indistinguishable to the dispatcher is the ambiguity the
// preview modal's removal resolves.
func bindingID(b key.Binding) string {
	h := b.Help()
	return strings.Join(b.Keys(), ",") + "|" + h.Key + "|" + h.Desc
}

// Contexts the registry is exercised against. Not exhaustive — one per
// shape the keybar can take, which is what the collision rule needs.
func representativeContexts() []struct {
	name string
	ctx  keyContext
} {
	return []struct {
		name string
		ctx  keyContext
	}{
		{"files/clean", keyContext{tab: TabTree, focus: PanelLeft}},
		{"files/modified", keyContext{tab: TabTree, focus: PanelRight, status: "M"}},
		{"files/conflict", keyContext{tab: TabTree, focus: PanelRight, status: "C"}},
		{"files/untracked", keyContext{tab: TabTree, focus: PanelRight, status: "?"}},
		{"files/marked", keyContext{tab: TabTree, focus: PanelLeft, marked: 3}},
		{"files/console-focus", keyContext{tab: TabTree, focus: PanelConsole}},
		{"favorites", keyContext{tab: TabFavorites, focus: PanelLeft}},
		{"staged/actions", keyContext{tab: TabStaged, focus: PanelLeft}},
		{"staged/conflict", keyContext{tab: TabStaged, focus: PanelLeft, status: "C"}},
		{"history/plain", keyContext{tab: TabHistory, focus: PanelLeft, histMode: HistoryContent}},
		{"history/diff", keyContext{tab: TabHistory, focus: PanelLeft, histMode: HistoryDiff}},
		{"history/compare", keyContext{tab: TabHistory, focus: PanelLeft, histMode: HistoryDiff, compare: true}},
		{"history/compare-pair", keyContext{tab: TabHistory, focus: PanelLeft, histMode: HistoryDiff, compare: true, pairMergeable: true}},
	}
}

// Every entry must be placeable: a keybar entry needs a segment its tab
// actually renders, and a help entry needs a section the help prints.
// An entry that satisfies neither is invisible, which is how issue 021's
// dead keys survived for so long.
func TestKeymapEntriesAreWellFormed(t *testing.T) {
	sections := make(map[string]bool, len(helpSectionOrder))
	for _, s := range helpSectionOrder {
		sections[s] = true
	}

	for i, e := range keyRegistry {
		id := bindingID(e.binding)
		if e.bar != 0 {
			if e.barSegment == "" {
				t.Errorf("entry %d (%s): bar rank %d without a segment", i, id, e.bar)
				continue
			}
			placed := false
			for _, tab := range []Tab{TabTree, TabFavorites, TabStaged, TabHistory} {
				if !e.inTab(tab) {
					continue
				}
				for _, seg := range append(barSegmentOrder(tab), "nav") {
					if seg == e.barSegment {
						placed = true
					}
				}
			}
			if !placed {
				t.Errorf("entry %d (%s): segment %q is not rendered by any tab it applies to",
					i, id, e.barSegment)
			}
		}
		if e.section != "" && !sections[e.section] {
			t.Errorf("entry %d (%s): section %q is not in helpSectionOrder", i, id, e.section)
		}
	}
}

// Two different actions must never claim the same key in the same
// situation. This is the check that would have caught p being both the
// preview modal and the side-by-side toggle.
func TestKeymapHasNoCollisions(t *testing.T) {
	for _, tc := range representativeContexts() {
		seen := map[string]string{} // key press -> description already claiming it
		for _, e := range keyRegistry {
			if e.bar == 0 || !e.live(tc.ctx) {
				continue
			}
			k, d := e.barText()
			if prev, dup := seen[k]; dup && prev != d {
				t.Errorf("%s: key %q claimed twice — %q and %q", tc.name, k, prev, d)
			}
			seen[k] = d
		}
	}
}

// Every binding in keyMap must be accounted for exactly once: either it
// is a registered user action, or it is widget-local, or it is a known
// orphan awaiting removal. No fourth category — an unlisted binding is
// how a key ends up bound, handled, and advertised nowhere.
func TestEveryBindingIsAccountedFor(t *testing.T) {
	registered := map[string]bool{}
	for _, e := range keyRegistry {
		registered[bindingID(e.binding)] = true
	}
	local := map[string]bool{}
	for _, b := range localBindings {
		local[bindingID(b)] = true
	}
	pending := map[string]bool{}
	for _, b := range pendingRemoval {
		pending[bindingID(b)] = true
	}

	v := reflect.ValueOf(keys)
	bindingType := reflect.TypeOf(key.Binding{})
	for i := 0; i < v.NumField(); i++ {
		if v.Field(i).Type() != bindingType {
			continue
		}
		name := v.Type().Field(i).Name
		b := v.Field(i).Interface().(key.Binding)
		id := bindingID(b)

		n := 0
		for _, in := range []bool{registered[id], local[id], pending[id]} {
			if in {
				n++
			}
		}
		switch n {
		case 1: // exactly right
		case 0:
			t.Errorf("keys.%s (%s) is in no list — add it to keyRegistry, "+
				"localBindings, or pendingRemoval", name, id)
		default:
			t.Errorf("keys.%s (%s) is listed %d times — it must be in exactly one list",
				name, id, n)
		}
	}
}

// The help modal is only a single source if it actually carries rows
// for every section it prints.
func TestHelpSectionsHaveRows(t *testing.T) {
	for _, section := range helpSectionOrder {
		rows := helpRows(section)
		if len(rows) == 0 {
			t.Errorf("help section %q has no rows", section)
		}
		for _, r := range rows {
			if r[0] == "" || r[1] == "" {
				t.Errorf("help section %q has an empty row: %q / %q", section, r[0], r[1])
			}
		}
	}
}

// The predicates are the part of the registry that carries logic, so
// they get exercised directly rather than only through a rendered row.
func TestKeymapPredicates(t *testing.T) {
	plain := keyContext{tab: TabHistory, histMode: HistoryContent}
	diff := keyContext{tab: TabHistory, histMode: HistoryDiff}
	compare := keyContext{tab: TabHistory, histMode: HistoryDiff, compare: true}
	pair := keyContext{tab: TabHistory, histMode: HistoryDiff, compare: true, pairMergeable: true}

	cases := []struct {
		name string
		got  bool
		want bool
	}{
		{"plain in content mode", historyPlain(plain), true},
		{"plain in diff mode", historyPlain(diff), false},
		{"plain while comparing", historyPlain(compare), false},
		{"side-by-side offered in diff mode", historyShowingDiff(diff), true},
		{"side-by-side offered while comparing", historyShowingDiff(compare), true},
		{"side-by-side not offered when browsing", historyShowingDiff(plain), false},
		{"single-file actions outside compare", outsideCompare(diff), true},
		{"single-file actions suppressed in compare", outsideCompare(compare), false},
		{"compare when comparing", historyCompare(compare), true},
		{"merge needs two real revisions", historyComparePair(compare), false},
		{"merge with a real pair", historyComparePair(pair), true},
		{"conflict predicate on C", onConflict(keyContext{status: "C"}), true},
		{"conflict predicate on M", onConflict(keyContext{status: "M"}), false},
		{"console predicate", inConsole(keyContext{focus: PanelConsole}), true},
		{"outside console", outsideConsole(keyContext{focus: PanelLeft}), true},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, c.got, c.want)
		}
	}
}
