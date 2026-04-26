package tui

import (
	"lazycvs/cvs"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type ConsoleFilter int

const (
	// ConsoleCompact is the default: one line per command (timestamp +
	// command + duration), failed commands rendered in red with the first
	// stderr line as a hint. Cuts noise in the small console panel and
	// makes "what just happened" obvious at a glance.
	ConsoleCompact ConsoleFilter = iota
	ConsoleAll                   // commands + full stdout/stderr/warn output
	ConsoleErrors                // only failed commands, full output
	ConsoleSlow                  // commands > 500ms, full output
)

type ConsoleModel struct {
	cmdLog   *cvs.CommandLog
	viewport viewport.Model
	width    int
	height   int
	lastLen  int
	filter   ConsoleFilter
}

func NewConsoleModel(cmdLog *cvs.CommandLog) ConsoleModel {
	vp := viewport.New(80, 6)
	return ConsoleModel{
		cmdLog:   cmdLog,
		viewport: vp,
		height:   6,
	}
}

func (m ConsoleModel) Update(msg tea.Msg) (ConsoleModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch {
		case key.Matches(msg, keys.Clear):
			m.cmdLog.Clear()
			m.lastLen = 0
			m.viewport.SetContent("")
			return m, nil
		case key.Matches(msg, keys.Filter):
			switch m.filter {
			case ConsoleCompact:
				m.filter = ConsoleAll
			case ConsoleAll:
				m.filter = ConsoleErrors
			case ConsoleErrors:
				m.filter = ConsoleSlow
			default:
				m.filter = ConsoleCompact
			}
			m.lastLen = 0 // force rebuild
			m.refreshContent()
			return m, nil
		}
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}

	// Refresh content from command log
	m.refreshContent()
	return m, nil
}

func (m ConsoleModel) FilterLabel() string {
	switch m.filter {
	case ConsoleAll:
		return "verbose"
	case ConsoleErrors:
		return "errors"
	case ConsoleSlow:
		return "slow"
	default:
		// ConsoleCompact — leave blank so the panel header reads just "Console".
		return ""
	}
}

func (m *ConsoleModel) refreshContent() {
	entries := m.cmdLog.All()
	if len(entries) == m.lastLen && m.lastLen > 0 {
		return
	}
	m.lastLen = len(entries)

	var b strings.Builder
	for _, e := range entries {
		// Apply skip-filter (Errors / Slow modes only show a subset).
		switch m.filter {
		case ConsoleErrors:
			if e.Success {
				continue
			}
		case ConsoleSlow:
			if e.Duration.Milliseconds() < 500 {
				continue
			}
		}

		// Compact mode: one line per command (timestamp + command + status).
		// Failed commands are colored red and get the first stderr line as
		// an inline hint so the user doesn't need to switch to verbose just
		// to see what went wrong.
		if m.filter == ConsoleCompact {
			ts := consoleTime.Render(e.Timestamp.Format("15:04:05"))
			cmdStyle := consoleCmd
			if !e.Success {
				cmdStyle = consoleErr
			}
			fmt.Fprintf(&b, "%s  %s", ts, cmdStyle.Render("$ "+e.Command))
			if e.Success {
				fmt.Fprintf(&b, "  %s\n", consoleStatus.Render(fmt.Sprintf("(%s)", e.Duration.Round(1e6))))
			} else {
				fmt.Fprintf(&b, "  %s\n", consoleErr.Render(fmt.Sprintf("FAILED %d", e.ExitCode)))
				if len(e.StderrLines) > 0 {
					fmt.Fprintf(&b, "    %s\n", consoleErr.Render("→ "+e.StderrLines[0]))
				}
			}
			continue
		}

		// Verbose / Errors / Slow: full output for each entry.
		ts := consoleTime.Render(e.Timestamp.Format("15:04:05"))
		cmd := consoleCmd.Render("$ " + e.Command)
		fmt.Fprintf(&b, "%s  %s\n", ts, cmd)

		for _, line := range e.StdoutLines {
			b.WriteString(consoleOk.Render(line) + "\n")
		}
		for _, line := range e.WarnLines {
			b.WriteString(consoleWarn.Render(line) + "\n")
		}
		for _, line := range e.StderrLines {
			b.WriteString(consoleErr.Render(line) + "\n")
		}

		var status string
		if e.Success {
			status = consoleStatus.Render(fmt.Sprintf("ok (took %s)", e.Duration))
		} else {
			status = consoleErr.Render(fmt.Sprintf("FAILED (exit %d) — %s", e.ExitCode, e.Duration))
		}
		b.WriteString(status + "\n")
	}

	m.viewport.SetContent(b.String())
	m.viewport.GotoBottom()
}

func (m *ConsoleModel) SetSize(width, height int) {
	m.width = width
	m.height = height
	m.viewport.Width = width
	m.viewport.Height = height
}

func (m ConsoleModel) View() string {
	style := lipgloss.NewStyle().
		Width(m.width).
		Height(m.height)
	if m.lastLen == 0 {
		return style.Foreground(colorMuted).Render("  No commands yet")
	}
	return m.viewport.View()
}
