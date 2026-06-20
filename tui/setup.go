package tui

import (
	"fmt"
	"lazycvs/cvs"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Bootstrap-checkout dialog: two-phase flow driven from `lazycvs init`.
//
//   Phase 1 (DialogCheckoutRoot)   — text input for CVSROOT. Enter
//     emits modulesLoadRequestedMsg; success transitions to phase 2,
//     failure stays here with checkoutErr surfaced under the input.
//
//   Phase 2 (DialogCheckoutModule) — list of modules from cvs co -c.
//     Cursor + Enter emit bootstrapCheckoutMsg; success closes the
//     dialog and quits the program (main re-enters with the new
//     working copy), failure stays here with checkoutErr surfaced
//     under the list.

// modulesLoadRequestedMsg fires when the user accepts the CVSROOT in
// the prompt. The App handler runs cvs.ListModules off the UI
// goroutine and re-injects modulesLoadedMsg with the result.
type modulesLoadRequestedMsg struct {
	root string
}

// modulesLoadedMsg carries the result of cvs.ListModules. err==nil
// means the dialog can advance to the picker; non-nil leaves it on
// the root prompt with the error visible.
type modulesLoadedMsg struct {
	root    string
	modules []cvs.Module
	err     error
}

// bootstrapCheckoutMsg fires when the user picks a module. The App
// handler runs cvs.CheckoutModule in parentDir and re-injects
// bootstrapCheckoutDoneMsg.
type bootstrapCheckoutMsg struct {
	root   string
	module cvs.Module
}

// bootstrapCheckoutDoneMsg ends the bootstrap. workDir is the
// absolute path of the new working copy on success; err non-nil
// keeps the picker open with the error.
type bootstrapCheckoutDoneMsg struct {
	workDir string
	err     error
}

// updateCheckoutRoot handles keys in the CVSROOT-input phase.
// Enter submits, Esc / Ctrl-C / q-while-not-typing quits the program.
// Everything else delegates to the textinput.Model via Update().
func (m DialogModel) updateCheckoutRoot(msg tea.KeyMsg) (DialogModel, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Escape):
		// Abort the bootstrap entirely. The App's setupMode catches
		// this when the dialog kind flips back to None and quits.
		m.Close()
		return m, tea.Quit
	case key.Matches(msg, keys.Enter):
		root := m.CheckoutRoot()
		if root == "" {
			m.checkoutErr = "CVSROOT is empty — enter a value like :pserver:user@host:/path"
			return m, nil
		}
		m.SetCheckoutLoading("Loading modules from " + root + "…")
		return m, func() tea.Msg {
			return modulesLoadRequestedMsg{root: root}
		}
	}
	// Fall through: input handles the keystroke.
	return m, nil
}

// updateCheckoutModule handles keys in the module-picker phase.
func (m DialogModel) updateCheckoutModule(msg tea.KeyMsg) (DialogModel, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Escape):
		m.Close()
		return m, tea.Quit
	case key.Matches(msg, keys.Up):
		if m.checkoutCursor > 0 {
			m.checkoutCursor--
		}
	case key.Matches(msg, keys.Down):
		if m.checkoutCursor < len(m.checkoutModules)-1 {
			m.checkoutCursor++
		}
	case key.Matches(msg, keys.Top):
		m.checkoutCursor = 0
	case key.Matches(msg, keys.Bottom):
		m.checkoutCursor = len(m.checkoutModules) - 1
	case key.Matches(msg, keys.Enter):
		mod := m.CheckoutSelectedModule()
		if mod == nil {
			return m, nil
		}
		// Snap a local copy — the closure outlives the receiver.
		picked := *mod
		root := m.CheckoutRoot()
		m.SetCheckoutLoading("Checking out " + picked.Name + "…")
		return m, func() tea.Msg {
			return bootstrapCheckoutMsg{root: root, module: picked}
		}
	}
	return m, nil
}

func (m DialogModel) viewCheckoutRoot() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Connect to CVS") + "\n\n")
	b.WriteString("CVSROOT:\n")
	b.WriteString(m.input.View() + "\n")
	b.WriteString(checkoutStatusLines(m.checkoutLoading, m.checkoutErr))
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("enter:list modules   esc:cancel"))
	return b.String()
}

func (m DialogModel) viewCheckoutModule() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Pick a module to check out") + "\n\n")
	b.WriteString(mutedStyle.Render("from "+m.CheckoutRoot()) + "\n\n")
	for i, mod := range m.checkoutModules {
		marker := "  "
		line := fmt.Sprintf("%s %-22s", marker, mod.Name)
		if mod.CheckoutDir != mod.Name {
			line += mutedStyle.Render(fmt.Sprintf("→ %s/  ", mod.CheckoutDir))
		}
		if mod.Definition != "" && mod.Definition != mod.Name {
			line += mutedStyle.Render(mod.Definition)
		}
		if i == m.checkoutCursor {
			line = lipgloss.NewStyle().Reverse(true).Render(line)
		}
		b.WriteString(line + "\n")
	}
	b.WriteString(checkoutStatusLines(m.checkoutLoading, m.checkoutErr))
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("j/k:nav  enter:checkout  esc:cancel"))
	return b.String()
}

// checkoutStatusLines renders the optional "loading…" line and / or
// the persistent error line beneath either phase. Returns "" when
// neither is set so the caller's spacing stays tight.
func checkoutStatusLines(loading, errMsg string) string {
	var lines []string
	if loading != "" {
		lines = append(lines, mutedStyle.Render(loading))
	}
	if errMsg != "" {
		lines = append(lines, lipgloss.NewStyle().Foreground(colorConflict).Render("✗ "+errMsg))
	}
	if len(lines) == 0 {
		return ""
	}
	return "\n" + strings.Join(lines, "\n") + "\n"
}
