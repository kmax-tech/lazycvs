package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type TreeNode struct {
	Name     string
	Path     string
	IsDir    bool
	Children []*TreeNode
	Expanded bool
	Status   string // "M", "C", "U", "?", ""
	Size     int64
	Counts   StatusCounts
}

type StatusCounts struct {
	Modified  int
	Conflict  int
	Updated   int
	Untracked int
}

func (c StatusCounts) String() string {
	var parts []string
	if c.Modified > 0 {
		parts = append(parts, fmt.Sprintf("%dM", c.Modified))
	}
	if c.Conflict > 0 {
		parts = append(parts, fmt.Sprintf("%dC", c.Conflict))
	}
	if c.Updated > 0 {
		parts = append(parts, fmt.Sprintf("%dU", c.Updated))
	}
	if c.Untracked > 0 {
		parts = append(parts, fmt.Sprintf("%d?", c.Untracked))
	}
	return strings.Join(parts, " ")
}

func (c StatusCounts) IsZero() bool {
	return c.Modified == 0 && c.Conflict == 0 && c.Updated == 0 && c.Untracked == 0
}

type flatNode struct {
	node  *TreeNode
	depth int
}

type TreeModel struct {
	root      []*TreeNode
	flat      []flatNode
	cursor    int
	workDir   string
	width     int
	height    int
	offset    int
	showFiles bool // when false, tree only shows directories (Variant A)
}

func NewTreeModel(workDir string) TreeModel {
	return TreeModel{
		workDir: workDir,
	}
}

func (m TreeModel) Init() tea.Cmd {
	return func() tea.Msg {
		return treeLoadedMsg{nodes: scanDir(m.workDir, ".")}
	}
}

type treeLoadedMsg struct{ nodes []*TreeNode }
type treeDirLoadedMsg struct{ path string; nodes []*TreeNode }

func (m TreeModel) Update(msg tea.Msg) (TreeModel, tea.Cmd) {
	switch msg := msg.(type) {
	case treeLoadedMsg:
		rootNode := &TreeNode{
			Name:     filepath.Base(m.workDir),
			Path:     ".",
			IsDir:    true,
			Expanded: true,
			Children: msg.nodes,
		}
		m.root = []*TreeNode{rootNode}
		m.rebuildFlat()
		return m, nil

	case treeDirLoadedMsg:
		if node := m.findNode(msg.path); node != nil {
			node.Children = msg.nodes
			node.Expanded = true
			m.rebuildFlat()
		}
		return m, nil

	case tea.KeyMsg:
		switch {
		case key.Matches(msg, keys.Down):
			if m.cursor < len(m.flat)-1 {
				m.cursor++
				m.ensureVisible()
			}
		case key.Matches(msg, keys.Up):
			if m.cursor > 0 {
				m.cursor--
				m.ensureVisible()
			}
		case key.Matches(msg, keys.Top):
			m.cursor = 0
			m.offset = 0
		case key.Matches(msg, keys.Bottom):
			m.cursor = max(0, len(m.flat)-1)
			m.ensureVisible()
		case key.Matches(msg, keys.Enter), key.Matches(msg, keys.Right):
			return m, m.expandOrSelect()
		case key.Matches(msg, keys.Left):
			m.collapseOrUp()
		case msg.String() == "~":
			// Collapse all and go to root
			for _, n := range m.root {
				m.collapseAll(n)
			}
			m.rebuildFlat()
			m.cursor = 0
			m.offset = 0
		}
	}
	return m, nil
}

func (m *TreeModel) expandOrSelect() tea.Cmd {
	if m.cursor >= len(m.flat) {
		return nil
	}
	node := m.flat[m.cursor].node
	if !node.IsDir {
		return nil
	}
	if node.Expanded {
		node.Expanded = false
		m.rebuildFlat()
		return nil
	}
	if node.Children != nil {
		node.Expanded = true
		m.rebuildFlat()
		return nil
	}
	// Lazy-load children
	path := node.Path
	workDir := m.workDir
	return func() tea.Msg {
		return treeDirLoadedMsg{path: path, nodes: scanDir(workDir, path)}
	}
}

func (m *TreeModel) collapseOrUp() {
	if m.cursor >= len(m.flat) {
		return
	}
	node := m.flat[m.cursor].node
	if node.IsDir && node.Expanded {
		node.Expanded = false
		m.rebuildFlat()
		return
	}
	// Move to parent
	depth := m.flat[m.cursor].depth
	for i := m.cursor - 1; i >= 0; i-- {
		if m.flat[i].depth < depth && m.flat[i].node.IsDir {
			m.cursor = i
			m.ensureVisible()
			return
		}
	}
}

// CanExpand reports whether the cursor is on a node where pressing `l` would
// do something (expand a collapsed dir, or load+expand its children). Used by
// the App-level panel-switch logic to decide whether `l` should be consumed
// by the tree or fall through to a focus-right action.
func (m TreeModel) CanExpand() bool {
	if m.cursor >= len(m.flat) {
		return false
	}
	node := m.flat[m.cursor].node
	if !node.IsDir {
		return false
	}
	return !node.Expanded
}

// CanCollapse reports whether the cursor is on a node where pressing `h`
// would do something (collapse the current dir, or move cursor up to a
// parent dir).
func (m TreeModel) CanCollapse() bool {
	if m.cursor >= len(m.flat) {
		return false
	}
	node := m.flat[m.cursor].node
	if node.IsDir && node.Expanded {
		return true
	}
	// Cursor has a parent dir to move up to iff there's a flat node above
	// at a shallower depth.
	depth := m.flat[m.cursor].depth
	for i := m.cursor - 1; i >= 0; i-- {
		if m.flat[i].depth < depth && m.flat[i].node.IsDir {
			return true
		}
	}
	return false
}

func (m TreeModel) SelectedPath() string {
	if m.cursor >= len(m.flat) {
		return ""
	}
	return m.flat[m.cursor].node.Path
}

func (m TreeModel) SelectedNode() *TreeNode {
	if m.cursor >= len(m.flat) {
		return nil
	}
	return m.flat[m.cursor].node
}

func (m *TreeModel) SetSize(width, height int) {
	m.width = width
	m.height = height
}

// applyStatusToNodes annotates each TreeNode with its status and aggregate
// counts, using the App-owned statusMap as the read-only source of truth.
// Directory counts are computed bottom-up: sum children's counts, then add
// statusMap entries not covered by any loaded child node.
func (m *TreeModel) applyStatusToNodes(nodes []*TreeNode, statusMap map[string]string) {
	for _, n := range nodes {
		if s, ok := statusMap[n.Path]; ok {
			n.Status = s
		} else {
			n.Status = ""
		}
		n.Counts = StatusCounts{}
		if n.IsDir {
			m.applyStatusToNodes(n.Children, statusMap)
			// Sum loaded children (their counts already include their subtrees).
			coveredPrefixes := make([]string, 0, len(n.Children))
			for _, child := range n.Children {
				n.Counts.add(child.Counts)
				addStatusToCount(&n.Counts, child.Status)
				if child.IsDir {
					coveredPrefixes = append(coveredPrefixes, child.Path+"/")
				} else {
					coveredPrefixes = append(coveredPrefixes, child.Path)
				}
			}
			// Add statusMap entries under this directory not covered by any
			// loaded child. This handles untracked files and unloaded subdirs.
			prefix := n.Path + "/"
			if n.Path == "." {
				prefix = ""
			}
			for path, status := range statusMap {
				if !strings.HasPrefix(path, prefix) {
					continue
				}
				covered := false
				for _, cp := range coveredPrefixes {
					if path == cp || strings.HasPrefix(path, cp) {
						covered = true
						break
					}
				}
				if !covered {
					addStatusToCount(&n.Counts, status)
				}
			}
		}
	}
}

func (c *StatusCounts) add(other StatusCounts) {
	c.Modified += other.Modified
	c.Conflict += other.Conflict
	c.Updated += other.Updated
	c.Untracked += other.Untracked
}

func addStatusToCount(c *StatusCounts, status string) {
	switch status {
	case "M":
		c.Modified++
	case "C":
		c.Conflict++
	case "U", "P":
		c.Updated++
	case "?":
		c.Untracked++
	}
}

func (m *TreeModel) rebuildFlat() {
	m.flat = nil
	m.flatten(m.root, 0)
	if m.cursor >= len(m.flat) {
		m.cursor = max(0, len(m.flat)-1)
	}
}

// RefreshStatus annotates every node with the new statusMap and rebuilds
// the flattened display list. Callers used to do these two steps in
// sequence everywhere; bundling them keeps the order correct (nodes must
// be annotated before flattening, so aggregate counts on directories are
// up to date) and prevents the silent-display-bug class where one of the
// two calls is forgotten.
func (m *TreeModel) RefreshStatus(statusMap map[string]string) {
	m.applyStatusToNodes(m.root, statusMap)
	m.rebuildFlat()
}

func (m *TreeModel) flatten(nodes []*TreeNode, depth int) {
	for _, n := range nodes {
		if !n.IsDir && !m.showFiles {
			continue
		}
		m.flat = append(m.flat, flatNode{node: n, depth: depth})
		if n.IsDir && n.Expanded {
			m.flatten(n.Children, depth+1)
		}
	}
}

// ExpandToPath expands all directories along the given relative path
// and positions the cursor at the target. Returns expanded directory paths
// that need status loading.
func (m *TreeModel) ExpandToPath(relPath string) []string {
	if relPath == "" || relPath == "." {
		return nil
	}
	parts := strings.Split(relPath, "/")
	nodes := m.root
	if len(nodes) == 1 && nodes[0].Path == "." {
		nodes = nodes[0].Children
	}
	var expanded []string

	for _, part := range parts {
		found := false
		for _, n := range nodes {
			if n.Name == part && n.IsDir {
				if n.Children == nil {
					n.Children = scanDir(m.workDir, n.Path)
				}
				n.Expanded = true
				expanded = append(expanded, n.Path)
				nodes = n.Children
				found = true
				break
			}
		}
		if !found {
			break
		}
	}

	m.rebuildFlat()

	// Position cursor at target path
	for i, fn := range m.flat {
		if fn.node.Path == relPath {
			m.cursor = i
			m.ensureVisible()
			return expanded
		}
	}
	return expanded
}

// ExpandedDirs returns paths of all currently expanded directories.
func (m *TreeModel) ExpandedDirs() []string {
	var dirs []string
	m.collectExpanded(m.root, &dirs)
	return dirs
}

func (m *TreeModel) collectExpanded(nodes []*TreeNode, dirs *[]string) {
	for _, n := range nodes {
		if n.IsDir && n.Expanded {
			*dirs = append(*dirs, n.Path)
			m.collectExpanded(n.Children, dirs)
		}
	}
}

func (m *TreeModel) collapseAll(node *TreeNode) {
	node.Expanded = false
	for _, c := range node.Children {
		if c.IsDir {
			m.collapseAll(c)
		}
	}
}

func (m *TreeModel) ensureVisible() {
	m.offset = ensureCursorVisible(m.cursor, m.offset, m.height)
}

func (m *TreeModel) findNode(path string) *TreeNode {
	return findInNodes(m.root, path)
}

func findInNodes(nodes []*TreeNode, path string) *TreeNode {
	for _, n := range nodes {
		if n.Path == path {
			return n
		}
		if n.IsDir {
			if found := findInNodes(n.Children, path); found != nil {
				return found
			}
		}
	}
	return nil
}

func (m TreeModel) View() string {
	if len(m.flat) == 0 {
		return mutedStyle.Render("  Loading...")
	}

	var lines []string
	end := min(m.offset+m.height, len(m.flat))
	for i := m.offset; i < end; i++ {
		f := m.flat[i]
		indent := strings.Repeat("  ", f.depth)

		var icon, name, counts string
		if f.node.IsDir {
			if f.node.Expanded {
				icon = "v "
			} else {
				icon = "> "
			}
			name = f.node.Name + "/"
			if !f.node.Counts.IsZero() {
				counts = "  " + renderCounts(f.node.Counts)
			}
		} else {
			icon = "  "
			name = f.node.Name
		}

		var statusStr string
		if f.node.Status != "" {
			c := statusColor(f.node.Status)
			statusStr = lipgloss.NewStyle().Foreground(c).Render(f.node.Status) + " "
		}

		line := indent + icon + statusStr + name + counts

		if i == m.cursor {
			line = lipgloss.NewStyle().Reverse(true).Render(line)
		}
		// Truncate to width
		if m.width > 0 && lipgloss.Width(line) > m.width {
			line = line[:m.width]
		}
		lines = append(lines, line)
	}

	// Pad remaining lines
	for len(lines) < m.height {
		lines = append(lines, "")
	}

	return strings.Join(lines, "\n")
}

func renderCounts(c StatusCounts) string {
	var parts []string
	if c.Modified > 0 {
		parts = append(parts, lipgloss.NewStyle().Foreground(colorMod).Render(fmt.Sprintf("%dM", c.Modified)))
	}
	if c.Conflict > 0 {
		parts = append(parts, lipgloss.NewStyle().Foreground(colorConflict).Render(fmt.Sprintf("%dC", c.Conflict)))
	}
	if c.Updated > 0 {
		parts = append(parts, lipgloss.NewStyle().Foreground(colorUpdated).Render(fmt.Sprintf("%dU", c.Updated)))
	}
	if c.Untracked > 0 {
		parts = append(parts, lipgloss.NewStyle().Foreground(colorUntracked).Render(fmt.Sprintf("%d?", c.Untracked)))
	}
	return strings.Join(parts, " ")
}

func scanDir(workDir, relPath string) []*TreeNode {
	absPath := filepath.Join(workDir, relPath)
	entries, err := os.ReadDir(absPath)
	if err != nil {
		return nil
	}

	var dirs, files []*TreeNode
	for _, e := range entries {
		name := e.Name()
		if skipInListing(name) {
			continue
		}

		path := name
		if relPath != "." {
			path = relPath + "/" + name
		}

		node := &TreeNode{
			Name:  name,
			Path:  path,
			IsDir: e.IsDir(),
		}

		if !e.IsDir() {
			if info, err := e.Info(); err == nil {
				node.Size = info.Size()
			}
		}

		if e.IsDir() {
			dirs = append(dirs, node)
		} else {
			files = append(files, node)
		}
	}

	sort.Slice(dirs, func(i, j int) bool { return dirs[i].Name < dirs[j].Name })
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })

	return append(dirs, files...)
}
