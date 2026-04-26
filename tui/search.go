package tui

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sahilm/fuzzy"
)

type SearchModel struct {
	input    textinput.Model
	allPaths []string
	results  []string
	cursor   int
	width    int
	height   int
	active   bool
}

func NewSearchModel() SearchModel {
	ti := textinput.New()
	ti.Placeholder = "Search files..."
	ti.CharLimit = 256
	return SearchModel{input: ti}
}

func (m *SearchModel) Open(workDir string) tea.Cmd {
	m.active = true
	m.input.Focus()
	m.input.SetValue("")
	m.results = nil
	m.cursor = 0

	if len(m.allPaths) == 0 {
		wd := workDir
		return func() tea.Msg {
			return pathsCollectedMsg{paths: collectPaths(wd)}
		}
	}
	return nil
}

func (m *SearchModel) Close() {
	m.active = false
	m.input.Blur()
}

type pathsCollectedMsg struct{ paths []string }
type searchSelectedMsg struct{ path string }

func (m SearchModel) Update(msg tea.Msg) (SearchModel, tea.Cmd) {
	switch msg := msg.(type) {
	case pathsCollectedMsg:
		m.allPaths = msg.paths
		return m, nil

	case tea.KeyMsg:
		switch {
		case key.Matches(msg, keys.Escape):
			m.Close()
			return m, nil
		case key.Matches(msg, keys.Enter):
			if m.cursor < len(m.results) {
				path := m.results[m.cursor]
				m.Close()
				return m, func() tea.Msg {
					return searchSelectedMsg{path: path}
				}
			}
			return m, nil
		case key.Matches(msg, keys.Down):
			if m.cursor < len(m.results)-1 {
				m.cursor++
			}
			return m, nil
		case key.Matches(msg, keys.Up):
			if m.cursor > 0 {
				m.cursor--
			}
			return m, nil
		}

		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)

		// Re-run fuzzy match
		query := m.input.Value()
		if query == "" {
			m.results = nil
		} else {
			matches := fuzzy.Find(query, m.allPaths)
			m.results = nil
			for _, match := range matches {
				m.results = append(m.results, match.Str)
				if len(m.results) >= 20 {
					break
				}
			}
		}
		m.cursor = 0
		return m, cmd
	}
	return m, nil
}

func (m SearchModel) View() string {
	if !m.active {
		return ""
	}

	boxWidth := min(m.width-4, 60)
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorActive).
		Padding(1, 2).
		Width(boxWidth)

	var content strings.Builder
	content.WriteString(titleStyle.Render("Search") + "\n\n")
	content.WriteString(m.input.View() + "\n\n")

	if len(m.results) == 0 && m.input.Value() != "" {
		content.WriteString(mutedStyle.Render("No matches"))
	}
	for i, path := range m.results {
		if i >= 15 {
			content.WriteString(mutedStyle.Render("..."))
			break
		}
		line := "  " + path
		if i == m.cursor {
			line = lipgloss.NewStyle().Reverse(true).Render(line)
		}
		content.WriteString(line + "\n")
	}

	content.WriteString("\n" + helpStyle.Render("enter:go  esc:cancel"))

	return boxStyle.Render(content.String())
}

func collectPaths(workDir string) []string {
	var paths []string
	filepath.Walk(workDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		name := info.Name()
		if name == "CVS" || name == ".cvsignore" || name == ".DS_Store" || strings.HasPrefix(name, ".#") {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(workDir, path)
		if rel != "." {
			paths = append(paths, rel)
		}
		return nil
	})
	return paths
}
