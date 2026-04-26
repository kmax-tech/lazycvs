package cvs

import "time"

// FileEntry represents a file in the CVS working copy with its status.
type FileEntry struct {
	Path    string    `json:"path"`
	Status  string    `json:"status"` // "M", "C", "U", "?", "P"
	IsDir   bool      `json:"is_dir"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
}

// InTheWayEntry represents a file that blocks a server update ("move away").
type InTheWayEntry struct {
	Path      string `json:"path"`
	LocalPath string `json:"local_path"`
}

// UpdateResult holds the parsed result of a CVS update operation.
type UpdateResult struct {
	Updated   []FileEntry    `json:"updated"`
	Modified  []FileEntry    `json:"modified"`
	Conflicts []FileEntry    `json:"conflicts"`
	Untracked []FileEntry    `json:"untracked"`
	InTheWay  []InTheWayEntry `json:"in_the_way"`
	StaleDirs []string       `json:"stale_dirs"`
	Errors    []string       `json:"errors"`
	Duration  time.Duration  `json:"duration"`
}

// StatusSummary provides aggregate counts for the dashboard.
type StatusSummary struct {
	ModifiedCount  int `json:"modified"`
	ConflictCount  int `json:"conflicts"`
	UntrackedCount int `json:"untracked"`
	StaleDirCount  int `json:"stale_dirs"`
	UpdatedCount   int `json:"updated"`
	InTheWayCount  int `json:"in_the_way"`
}

// Summary returns aggregate counts from an UpdateResult.
func (r *UpdateResult) Summary() StatusSummary {
	return StatusSummary{
		ModifiedCount:  len(r.Modified),
		ConflictCount:  len(r.Conflicts),
		UntrackedCount: len(r.Untracked),
		StaleDirCount:  len(r.StaleDirs),
		UpdatedCount:   len(r.Updated),
		InTheWayCount:  len(r.InTheWay),
	}
}

// AllFiles returns all file entries combined for filtering/display.
func (r *UpdateResult) AllFiles() []FileEntry {
	all := make([]FileEntry, 0, len(r.Updated)+len(r.Modified)+len(r.Conflicts)+len(r.Untracked))
	all = append(all, r.Updated...)
	all = append(all, r.Modified...)
	all = append(all, r.Conflicts...)
	all = append(all, r.Untracked...)
	return all
}

// CommandResult holds the result of a single CVS command execution.
type CommandResult struct {
	Command   string        `json:"command"`
	Args      []string      `json:"args"`
	Stdout    string        `json:"stdout"`
	Stderr    string        `json:"stderr"`
	ExitCode  int           `json:"exit_code"`
	Duration  time.Duration `json:"duration"`
	Timestamp time.Time     `json:"timestamp"`
	WorkDir   string        `json:"work_dir"`
	Success   bool          `json:"success"`
}
