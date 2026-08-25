package tui

import (
	"fmt"
	"github.com/kmax-tech/lazycvs/config"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type FavoriteEntry struct {
	Config config.FavoriteDir
	Counts StatusCounts
}

type FavoritesModel struct {
	favorites []FavoriteEntry
	cursor    int
	width     int
	height    int
}

func NewFavoritesModel(cfg *config.Config) FavoritesModel {
	m := FavoritesModel{}
	if cfg != nil {
		for _, d := range cfg.Favorites.Dirs {
			m.favorites = append(m.favorites, FavoriteEntry{Config: d})
		}
	}
	return m
}

func (m FavoritesModel) Update(msg tea.Msg) (FavoritesModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch {
		case key.Matches(msg, keys.Down):
			if m.cursor < len(m.favorites)-1 {
				m.cursor++
			}
		case key.Matches(msg, keys.Up):
			if m.cursor > 0 {
				m.cursor--
			}
		case key.Matches(msg, keys.Top):
			m.cursor = 0
		case key.Matches(msg, keys.Bottom):
			m.cursor = max(0, len(m.favorites)-1)
		case key.Matches(msg, keys.PageDown):
			m.cursor = clamp(m.cursor+m.height, 0, max(0, len(m.favorites)-1))
		case key.Matches(msg, keys.PageUp):
			m.cursor = clamp(m.cursor-m.height, 0, max(0, len(m.favorites)-1))
		}
	}
	return m, nil
}

func (m FavoritesModel) SelectedPath() string {
	if m.cursor >= len(m.favorites) {
		return ""
	}
	return m.favorites[m.cursor].Config.Path
}

// Contains reports whether path is already favorited.
func (m FavoritesModel) Contains(path string) bool {
	for _, f := range m.favorites {
		if f.Config.Path == path {
			return true
		}
	}
	return false
}

// Add appends an entry to the in-memory list. Persistence is the
// caller's job (App.addFavorite writes through ConfigManager).
func (m *FavoritesModel) Add(d config.FavoriteDir) {
	m.favorites = append(m.favorites, FavoriteEntry{Config: d})
}

// RemoveSelected drops the cursor entry and returns its path, or ""
// when the list is empty. The cursor is clamped so it stays on a
// valid row after the removal.
func (m *FavoritesModel) RemoveSelected() string {
	if m.cursor >= len(m.favorites) {
		return ""
	}
	path := m.favorites[m.cursor].Config.Path
	m.favorites = append(m.favorites[:m.cursor], m.favorites[m.cursor+1:]...)
	if m.cursor >= len(m.favorites) {
		m.cursor = max(0, len(m.favorites)-1)
	}
	return path
}

// SetSize takes the App's layout envelope. Favorites fills the left panel
// only, so it reads LeftW and Height; RightW is ignored.
func (m *FavoritesModel) SetSize(d PanelDims) {
	m.width = d.LeftW
	m.height = d.Height
}

func (m *FavoritesModel) UpdateCounts(statusMap map[string]string) {
	for i := range m.favorites {
		fav := &m.favorites[i]
		fav.Counts = StatusCounts{}
		prefix := fav.Config.Path + "/"
		for path, status := range statusMap {
			if strings.HasPrefix(path, prefix) || path == fav.Config.Path {
				switch status {
				case "M":
					fav.Counts.Modified++
				case "C":
					fav.Counts.Conflict++
				case "U", "P":
					fav.Counts.Updated++
				case "?":
					fav.Counts.Untracked++
				}
			}
		}
	}
}

func (m FavoritesModel) View() string {
	header := titleStyle.Render(" favorites")
	var lines []string
	lines = append(lines, header)

	if len(m.favorites) == 0 {
		lines = append(lines, mutedStyle.Render("  No favorites configured"))
		lines = append(lines, mutedStyle.Render("  Press + to add"))
		for len(lines) < m.height {
			lines = append(lines, "")
		}
		return strings.Join(lines, "\n")
	}

	for i, fav := range m.favorites {
		name := fav.Config.Name
		if name == "" {
			name = fav.Config.Path
		}

		counts := ""
		if !fav.Counts.IsZero() {
			counts = "  " + renderCounts(fav.Counts)
		} else {
			counts = mutedStyle.Render("  ok")
		}

		line := fmt.Sprintf("  %s%s", name, counts)
		if i == m.cursor {
			line = lipgloss.NewStyle().Reverse(true).Render(line)
		}
		lines = append(lines, line)
	}

	for len(lines) < m.height {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}
