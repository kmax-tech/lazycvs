package tui

import (
	"lazycvs/cvs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
)

// CVS status loading. The general flow is:
//   user action / startup → backgroundDirScan or refreshStagedFiles
//   → loadDirStatus / refreshStagedFiles dispatched per directory or path
//   → dirStatusMsg / stagedStatusMsg handlers in app_core.go merge into
//     m.statusMap via m.applyStatuses

// resolveFileStatus returns the CVS status code for path. If statusMap
// has no entry, walks every ancestor directory from the file's
// immediate parent up to the working-copy root, looking for a missing
// CVS/ marker. A *single* missing marker anywhere in the chain means
// the file is in a user-created subdirectory that hasn't been
// `cvs add`-ed yet — its files aren't tracked, so we report "?". The
// file is only treated as tracked-but-clean ("") when every ancestor
// has its own CVS/ marker.
//
// Stat'ing each ancestor is cheap (the filesystem caches inode lookups
// and CVS depths are usually shallow), so there's no artificial cap on
// the walk: an unbroken chain of CVS/ markers all the way to the root
// is the only condition that returns "".
func (m *App) resolveFileStatus(path string) string {
	if s := m.statusMap[path]; s != "" {
		return s
	}
	dir := filepath.Dir(path)
	for {
		cvsDir := filepath.Join(m.exec.WorkDir, dir, "CVS")
		if _, err := os.Stat(cvsDir); os.IsNotExist(err) {
			return "?"
		}
		if dir == "." || dir == "/" || dir == "" {
			break
		}
		dir = filepath.Dir(dir)
	}
	return ""
}

// statusesFor returns a path → status code map for the given paths,
// using resolveFileStatus per entry (so untracked files in
// CVS-less subdirs get "?" rather than ""). Used by the commit /
// remove dialogs and any other multi-path action that needs to
// display or branch on status per file.
func (m *App) statusesFor(paths []string) map[string]string {
	out := make(map[string]string, len(paths))
	for _, p := range paths {
		out[p] = m.resolveFileStatus(p)
	}
	return out
}

// refreshStagedFiles runs `cvs status` for the given file paths. CVS
// reports basenames only when status is invoked on individual files,
// so this remaps basenames back to the caller's full paths before
// returning the message.
func refreshStagedFiles(exec *cvs.CVSExecutor, paths []string) tea.Cmd {
	return func() tea.Msg {
		args := append([]string{"status"}, paths...)
		result, _ := exec.RunReadOnly(args...)
		if result == nil {
			return stagedStatusMsg{paths: paths}
		}
		statuses := cvs.ParseStatus(result.Stdout)
		// `cvs status <path>` reports the basename only ("File: notes.txt")
		// — no "Examining <dir>" prefix to give ParseStatus a directory
		// context. Remap basenames back to the original input paths so the
		// stagedStatusMsg handler keys statusMap correctly.
		byBase := make(map[string]string, len(paths))
		for _, p := range paths {
			byBase[filepath.Base(p)] = p
		}
		for i, s := range statuses {
			if full, ok := byBase[s.Path]; ok {
				statuses[i].Path = full
			}
		}
		return stagedStatusMsg{paths: paths, statuses: statuses}
	}
}

func loadDirStatus(executor *cvs.CVSExecutor, dir string, epoch uint64) tea.Cmd {
	return func() tea.Msg {
		args := []string{"status", "-l"}
		if dir != "." {
			args = append(args, dir)
		}
		result, _ := executor.RunReadOnly(args...)
		if result == nil {
			return dirStatusMsg{dir: dir, epoch: epoch}
		}
		return dirStatusMsg{dir: dir, statuses: cvs.ParseStatus(result.Stdout), epoch: epoch}
	}
}

// scanTargetFiles is the soft upper bound on how many CVS-managed
// files one cvs status invocation should cover. The partitioning
// algorithm splits the directory tree so each scan stays roughly at
// or below this number — large enough that the per-scan startup cost
// is amortized, small enough that we get useful parallelism on big
// working copies.
const scanTargetFiles = 200

// scanWorkerCount caps how many cvs status invocations run at once.
// Each one shares the executor's read lock, so they really do execute
// in parallel; the cap prevents fork-storm on huge repos.
const scanWorkerCount = 8

// scanSpec is one unit of work for the worker pool: scan dir, either
// recursively (for subtrees that fit within scanTargetFiles) or just
// the directory's own files (for nodes too big to scan as one chunk;
// their children become their own scanSpecs).
type scanSpec struct {
	dir       string
	recursive bool
}

// dirNode is the in-memory tree the partitioning algorithm walks.
// It only contains CVS-managed directories — non-CVS subtrees are
// pruned during the filesystem walk so they don't pollute the counts.
type dirNode struct {
	relPath  string // relative to working copy root, "." for root
	files    int    // direct file count (CVS-relevant; excludes hidden / build artifacts)
	subtree  int    // direct + descendants
	children []*dirNode
}

// buildDirTree walks the working copy under root, descending only into
// directories that have a CVS/ subdirectory. Returns the tree root or
// nil if root itself isn't CVS-managed. Cheap — pure filesystem stat,
// no cvs invocations.
func buildDirTree(workDir, scope string) *dirNode {
	rootAbs := workDir
	rootRel := "."
	if scope != "" && scope != "." {
		rootAbs = filepath.Join(workDir, scope)
		rootRel = scope
	}
	if _, err := os.Stat(filepath.Join(rootAbs, "CVS")); err != nil {
		return nil
	}
	return walkDirNode(rootAbs, rootRel)
}

func walkDirNode(absDir, relDir string) *dirNode {
	entries, err := os.ReadDir(absDir)
	if err != nil {
		return nil
	}
	n := &dirNode{relPath: relDir}
	for _, e := range entries {
		name := e.Name()
		if name == "CVS" || strings.HasPrefix(name, ".") {
			continue
		}
		if e.IsDir() {
			subAbs := filepath.Join(absDir, name)
			if _, err := os.Stat(filepath.Join(subAbs, "CVS")); err != nil {
				continue // not part of the CVS working copy
			}
			subRel := name
			if relDir != "." {
				subRel = relDir + "/" + name
			}
			if c := walkDirNode(subAbs, subRel); c != nil {
				n.children = append(n.children, c)
				n.subtree += c.subtree
			}
		} else if !skipInListing(name) {
			n.files++
		}
	}
	n.subtree += n.files
	return n
}

// partitionTree returns a list of scanSpecs covering every file in
// the tree exactly once. A subtree fitting within scanTargetFiles
// becomes one recursive scan; bigger subtrees are split — the parent
// directory's own files become a non-recursive scan and each child
// is partitioned independently.
//
// Tiny dirs (e.g. one with 3 files) never get their own scan — they
// roll up into the nearest ancestor whose subtree fits the target.
func partitionTree(n *dirNode) []scanSpec {
	if n == nil {
		return nil
	}
	var specs []scanSpec
	var visit func(*dirNode)
	visit = func(d *dirNode) {
		// A subtree fits as one recursive scan if the file count is
		// within budget OR there are no children to split into. Leaf
		// directories with more than scanTargetFiles still scan as
		// one chunk — there's nothing to break them up further.
		if d.subtree <= scanTargetFiles || len(d.children) == 0 {
			specs = append(specs, scanSpec{dir: d.relPath, recursive: true})
			return
		}
		// Too big as one chunk: scan this dir's direct files non-
		// recursively, then partition each child independently.
		if d.files > 0 {
			specs = append(specs, scanSpec{dir: d.relPath, recursive: false})
		}
		for _, c := range d.children {
			visit(c)
		}
	}
	visit(n)
	return specs
}

// backgroundDirScan partitions the working copy into balanced scan
// units (each ~scanTargetFiles files) and runs them concurrently with
// a bounded worker pool, then merges the results into one
// dirStatusMsg.
//
// Why this shape: a single recursive `cvs status` on a large repo is
// slow (one process scanning thousands of files); one cvs invocation
// per directory is the opposite extreme (fork-storm + quadratic tree
// refresh). Partitioning first by file count gives the right
// granularity — small dirs roll up into ancestor scans (no per-3-file
// cvs call), large dirs split into per-subdir scans that parallelize
// well.
//
// The partitioning walk is pure filesystem stat and runs entirely in
// the goroutine spawned by tea.Cmd, so it doesn't block the main loop.
// All cvs invocations happen via the executor (so they show up in the
// console panel) but stay capped at scanWorkerCount in flight.
//
// Targeted refreshes after individual actions still use loadDirStatus
// directly (via refreshStatusForPaths), which only touches the
// affected directories — unaffected by repo size.
func backgroundDirScan(executor *cvs.CVSExecutor, epoch uint64, scope string) tea.Cmd {
	dirLabel := scope
	if dirLabel == "" {
		dirLabel = "."
	}
	return func() tea.Msg {
		return dirStatusMsg{
			dir:       dirLabel,
			recursive: true,
			statuses:  runPartitionScan(executor, scope),
			epoch:     epoch,
		}
	}
}

// runPartitionScan executes the per-partition `cvs status` worker pool
// synchronously and returns the combined result. Shared between the
// background-scan Cmd and the parallel branch of refreshStatusUser, so
// both paths get identical partitioning + concurrency behavior.
func runPartitionScan(executor *cvs.CVSExecutor, scope string) []cvs.FileStatus {
	root := buildDirTree(executor.WorkDir, scope)
	specs := partitionTree(root)
	if len(specs) == 0 {
		return nil
	}

	var (
		mu       sync.Mutex
		combined []cvs.FileStatus
		wg       sync.WaitGroup
	)
	sem := make(chan struct{}, scanWorkerCount)
	for _, spec := range specs {
		wg.Add(1)
		go func(s scanSpec) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			args := []string{"status"}
			if !s.recursive {
				args = append(args, "-l")
			}
			if s.dir != "." {
				args = append(args, s.dir)
			}
			result, _ := executor.RunReadOnly(args...)
			if result == nil {
				return
			}
			parsed := cvs.ParseStatus(result.Stdout)
			if len(parsed) == 0 {
				return
			}
			mu.Lock()
			combined = append(combined, parsed...)
			mu.Unlock()
		}(spec)
	}
	wg.Wait()
	return combined
}


// cvsStatusCode maps the long-form status strings emitted by `cvs status`
// to the single-letter codes used throughout the UI. Returns "" for
// statuses that don't map to anything actionable (e.g. "Up-to-date").
func cvsStatusCode(status string) string {
	switch status {
	case "Locally Modified":
		return "M"
	case "Locally Added":
		return "A"
	case "Locally Removed":
		return "R"
	case "Needs Update", "Needs Patch":
		return "U"
	case "Needs Merge":
		return "M"
	case "File had conflicts on merge", "Unresolved Conflict":
		return "C"
	case "Unknown":
		// `cvs status` calls untracked files "Unknown" while `cvs update -n`
		// uses "?". Map both to the same code so the TUI shows ? consistently.
		return "?"
	default:
		return ""
	}
}
