package tui

import (
	"fmt"
	"github.com/kmax-tech/lazycvs/cvs"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
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

// ShowErrors switches the panel to the errors filter. Used when the
// user goes looking for the detail behind a failure banner: the banner
// says what failed, the console says why, and landing in the compact
// view with fifty successful commands between you and the error is not
// an answer.
func (m *ConsoleModel) ShowErrors() {
	m.filter = ConsoleErrors
	m.lastLen = 0 // force a rebuild
	m.refreshContent()
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
			b.WriteString(compactLine(e) + "\n")
			if !e.Success && len(e.StderrLines) > 0 {
				fmt.Fprintf(&b, "    %s\n", consoleErr.Render("→ "+e.StderrLines[0]))
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

// SetSize takes the App's layout envelope. The console spans the full
// terminal width below the main panels, so the App passes the total
// width via LeftW; RightW is unused.
func (m *ConsoleModel) SetSize(d PanelDims) {
	grew := d.Height != m.height
	m.width = d.LeftW
	m.height = d.Height
	m.viewport.Width = d.LeftW
	m.viewport.Height = d.Height
	if grew {
		// The scroll offset was computed for the old height. Collapsed
		// to one line it sits at the last line of the content; expanded
		// to six without this, the viewport scrolls past the end and
		// the panel renders blank — a console that opens empty right
		// when the user went looking for a failure.
		m.viewport.GotoBottom()
	}
}

func (m ConsoleModel) View() string {
	return m.ViewAt(m.height)
}

// ViewAt renders the console for the height the layout is actually
// drawing, rather than for the height SetSize last stored.
//
// The two differ: the panel collapses and expands on its own — an
// unread error opens it without the user touching anything — and that
// changes the layout on the very next frame, while SetSize only runs on
// a focus change or a terminal resize. Rendering from the stored height
// drew one collapsed line inside a six-row panel, which is precisely
// the frame a failure produces.
func (m ConsoleModel) ViewAt(height int) string {
	style := lipgloss.NewStyle().
		Width(m.width).
		Height(height)
	if m.lastLen == 0 {
		return style.Foreground(colorMuted).Render("  No commands yet")
	}
	if height <= consoleCollapsed {
		return m.collapsedView()
	}
	// The viewport is a value; resizing a copy keeps this a pure
	// render while still showing `height` lines of the log.
	vp := m.viewport
	vp.Height = height
	vp.GotoBottom()
	return vp.View()
}

// compactLine is one command as the compact filter shows it:
// timestamp, the command, and either its duration or its exit code.
// The collapsed panel shows the same line, so the console reads the
// same whether it is one line tall or six.
func compactLine(e cvs.ConsoleEntry) string {
	ts := consoleTime.Render(e.Timestamp.Format("15:04:05"))
	cmdStyle := consoleCmd
	if !e.Success {
		cmdStyle = consoleErr
	}
	line := ts + "  " + cmdStyle.Render("$ "+e.Command)
	if e.Success {
		return line + "  " + consoleStatus.Render(fmt.Sprintf("(%s)", e.Duration.Round(time.Millisecond)))
	}
	return line + "  " + consoleErr.Render(fmt.Sprintf("FAILED %d", e.ExitCode))
}

// collapsedView is the single line the console shows while it is out of
// the way: the command that just ran and how it went. The viewport
// cannot serve this — scrolled to the bottom, its last line is the
// status ("ok (took 11ms)") with the command itself one line above and
// out of sight.
func (m ConsoleModel) collapsedView() string {
	entries := m.cmdLog.All()
	if len(entries) == 0 {
		return mutedStyle.Render("  No commands yet")
	}
	return ansi.Truncate("  "+compactLine(entries[len(entries)-1]), max(1, m.width), "…")
}
