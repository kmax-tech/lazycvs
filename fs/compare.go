package fs

import (
	"github.com/kmax-tech/lazycvs/cvs"
	"os"
	"path/filepath"
	"strings"
)

// CompareEntry represents a file or directory in the compare tree.
type CompareEntry struct {
	Path        string         `json:"path"`
	Name        string         `json:"name"`
	Type        string         `json:"type"`   // "file" or "dir"
	Status      string         `json:"status"` // "identical", "modified", "local_only", "server_only", "conflict"
	StatusLabel string         `json:"status_label"`
	LocalSize   int64          `json:"local_size,omitempty"`
	Depth       int            `json:"depth"`
	Children    []CompareEntry `json:"children,omitempty"`
	HasChildren bool           `json:"has_children"`
}

var statusLabels = map[string]string{
	"Up-to-date":                  "identical",
	"Locally Modified":            "modified",
	"Locally Added":               "local_only",
	"Needs Checkout":              "server_only",
	"Needs Patch":                 "server_only",
	"Needs Merge":                 "conflict",
	"File had conflicts on merge": "conflict",
}

// Compare builds a top-level compare tree from CVS status data.
func Compare(workDir string, statuses []cvs.FileStatus) []CompareEntry {
	// Group by top-level directory
	dirs := make(map[string]bool)
	filesByDir := make(map[string][]cvs.FileStatus)

	for _, s := range statuses {
		parts := strings.SplitN(s.Path, "/", 2)
		if len(parts) == 2 {
			dirs[parts[0]] = true
			filesByDir[parts[0]] = append(filesByDir[parts[0]], s)
		} else {
			filesByDir["."] = append(filesByDir["."], s)
		}
	}

	var entries []CompareEntry

	// Add directories
	for dir := range dirs {
		entry := CompareEntry{
			Path:        dir,
			Name:        dir,
			Type:        "dir",
			Status:      dirStatus(filesByDir[dir]),
			HasChildren: true,
			Depth:       0,
		}
		entry.StatusLabel = entry.Status
		entries = append(entries, entry)
	}

	// Add root-level files
	for _, s := range filesByDir["."] {
		entry := CompareEntry{
			Path:   s.Path,
			Name:   s.Path,
			Type:   "file",
			Status: mapStatus(s.Status),
			Depth:  0,
		}
		entry.StatusLabel = entry.Status
		if info, err := os.Stat(filepath.Join(workDir, s.Path)); err == nil {
			entry.LocalSize = info.Size()
		}
		entries = append(entries, entry)
	}

	return entries
}

// CompareChildren builds children entries for a directory.
func CompareChildren(workDir, dir string, statuses []cvs.FileStatus) []CompareEntry {
	prefix := dir + "/"
	var entries []CompareEntry
	seen := make(map[string]bool)

	for _, s := range statuses {
		if !strings.HasPrefix(s.Path, prefix) {
			continue
		}

		rest := strings.TrimPrefix(s.Path, prefix)
		parts := strings.SplitN(rest, "/", 2)

		if len(parts) == 2 {
			// Subdirectory
			subDir := parts[0]
			if !seen[subDir] {
				seen[subDir] = true
				entries = append(entries, CompareEntry{
					Path:        dir + "/" + subDir,
					Name:        subDir,
					Type:        "dir",
					Status:      "identical",
					HasChildren: true,
					Depth:       1,
				})
			}
		} else {
			// Direct child file
			entry := CompareEntry{
				Path:   s.Path,
				Name:   parts[0],
				Type:   "file",
				Status: mapStatus(s.Status),
				Depth:  1,
			}
			entry.StatusLabel = entry.Status
			if info, err := os.Stat(filepath.Join(workDir, s.Path)); err == nil {
				entry.LocalSize = info.Size()
			}
			entries = append(entries, entry)
		}
	}

	return entries
}

func mapStatus(cvsStatus string) string {
	if label, ok := statusLabels[cvsStatus]; ok {
		return label
	}
	return "unknown"
}

func dirStatus(statuses []cvs.FileStatus) string {
	hasModified := false
	hasConflict := false
	for _, s := range statuses {
		mapped := mapStatus(s.Status)
		if mapped == "conflict" {
			hasConflict = true
		}
		if mapped == "modified" || mapped == "local_only" || mapped == "server_only" {
			hasModified = true
		}
	}
	if hasConflict {
		return "conflict"
	}
	if hasModified {
		return "modified"
	}
	return "identical"
}
