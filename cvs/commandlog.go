package cvs

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// ConsoleEntry is a CommandResult with classified output lines for display.
type ConsoleEntry struct {
	CommandResult
	StdoutLines []string `json:"stdout_lines"`
	StderrLines []string `json:"stderr_lines"`
	WarnLines   []string `json:"warn_lines"`
}

// warn patterns in stderr that get classified as warnings rather than errors
var warnPatterns = []string{
	"move away",
	"cannot open directory",
	"skipping directory",
	"scheduling",
}

// CommandLog is a thread-safe ring buffer of console entries with pub/sub for SSE.
type CommandLog struct {
	mu            sync.Mutex
	entries       []ConsoleEntry
	maxSize       int
	subscribers   map[chan ConsoleEntry]struct{}
	unreadErrors  int // failed-command count since last MarkRead()
	logFile       *os.File // optional append-only session log (EnableFileLog)
}

// NewCommandLog creates a new CommandLog with the given max size.
func NewCommandLog(maxSize int) *CommandLog {
	return &CommandLog{
		maxSize:     maxSize,
		entries:     make([]ConsoleEntry, 0, maxSize),
		subscribers: make(map[chan ConsoleEntry]struct{}),
	}
}

// Add records a command result and notifies all subscribers.
func (l *CommandLog) Add(result CommandResult) {
	entry := ConsoleEntry{CommandResult: result}

	// Stdout and Stderr now share the same combined output (the
	// executor merges them so the stream order matches cvs's actual
	// write order — see cvs/executor.go::run). Iterate once and
	// classify each line by content: cvs's "cvs <cmd>: …" progress /
	// warning messages count as warnings or stderr, the rest as data.
	for _, line := range splitLines(result.Stdout) {
		switch {
		case isWarnLine(line):
			entry.WarnLines = append(entry.WarnLines, line)
		case strings.HasPrefix(line, "cvs ") || strings.HasPrefix(line, "cvs:"):
			entry.StderrLines = append(entry.StderrLines, line)
		default:
			entry.StdoutLines = append(entry.StdoutLines, line)
		}
	}
	l.push(entry)
}

// LogFileOp records a filesystem operation lazycvs performed itself —
// delete, rename, backup restore. The cvs invocations alone don't tell
// the whole session story: reverts and conflict resolutions also rm /
// rename files directly, and "which file did lazycvs delete, and when?"
// must be answerable from the console and the session log. op reads
// like a shell command ("rm foo.jpg  # revert conflict") so it renders
// naturally next to the real ones. Nil-safe so helpers can log through
// executors that were built without a CommandLog (tests).
func (l *CommandLog) LogFileOp(op string, err error) {
	if l == nil {
		return
	}
	entry := ConsoleEntry{CommandResult: CommandResult{
		Command:   op,
		Success:   err == nil,
		Timestamp: time.Now(),
	}}
	if err != nil {
		entry.ExitCode = 1
		entry.StderrLines = []string{err.Error()}
	}
	l.push(entry)
}

// push appends a classified entry to the ring, mirrors it into the
// session log file (if enabled), and notifies subscribers.
func (l *CommandLog) push(entry ConsoleEntry) {
	l.mu.Lock()
	if len(l.entries) >= l.maxSize {
		l.entries = l.entries[1:]
	}
	l.entries = append(l.entries, entry)
	if !entry.Success {
		l.unreadErrors++
	}
	l.writeToFileLocked(entry)

	// Copy subscribers to avoid holding lock during send
	subs := make([]chan ConsoleEntry, 0, len(l.subscribers))
	for ch := range l.subscribers {
		subs = append(subs, ch)
	}
	l.mu.Unlock()

	// Notify subscribers (non-blocking)
	for _, ch := range subs {
		select {
		case ch <- entry:
		default:
		}
	}
}

// maxSessionLogBytes bounds the session log file. When it exceeds this
// at startup it's rotated to <path>.old (replacing the previous .old),
// so total disk usage stays at ~2× this size while the tail of the
// history is always preserved.
const maxSessionLogBytes = 1 << 20 // 1 MB ≈ a dozen thousand entries

// EnableFileLog opens path for appending and mirrors every subsequent
// entry into it, starting with a session marker line. Unlike the ring
// buffer (which evicts and can be cleared with `x`), the file is a
// rolling audit trail — the "session report" for questions like
// "which files did that bulk revert delete yesterday?".
func (l *CommandLog) EnableFileLog(path, workDir string) error {
	if info, err := os.Stat(path); err == nil && info.Size() > maxSessionLogBytes {
		// Best-effort rotation at session start; a session's own
		// writes are small, so checking only here keeps the hot path
		// free of size checks.
		_ = os.Rename(path, path+".old")
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	fmt.Fprintf(f, "\n=== lazycvs session %s — %s ===\n",
		time.Now().Format("2006-01-02 15:04:05"), workDir)
	l.mu.Lock()
	l.logFile = f
	l.mu.Unlock()
	return nil
}

// writeToFileLocked appends one entry to the session log. Successful
// commands get a single header line; failures also carry their warn /
// stderr lines (capped) so the report explains itself without the TUI.
// Caller holds l.mu.
func (l *CommandLog) writeToFileLocked(e ConsoleEntry) {
	if l.logFile == nil {
		return
	}
	status := "ok"
	if !e.Success {
		status = fmt.Sprintf("FAILED exit=%d", e.ExitCode)
	}
	dur := ""
	if e.Duration > 0 {
		dur = fmt.Sprintf(" (%s)", e.Duration.Round(time.Millisecond))
	}
	fmt.Fprintf(l.logFile, "%s $ %s%s %s\n", e.Timestamp.Format("15:04:05"), e.Command, dur, status)
	if e.Success {
		return
	}
	const maxLines = 20
	n := 0
	for _, line := range append(append([]string{}, e.WarnLines...), e.StderrLines...) {
		if n >= maxLines {
			fmt.Fprintf(l.logFile, "    … (%d more lines)\n", len(e.WarnLines)+len(e.StderrLines)-n)
			break
		}
		fmt.Fprintf(l.logFile, "    %s\n", line)
		n++
	}
}

// All returns a copy of all entries.
func (l *CommandLog) All() []ConsoleEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]ConsoleEntry, len(l.entries))
	copy(out, l.entries)
	return out
}

// Last returns the most recent entry, or nil if empty.
func (l *CommandLog) Last() *ConsoleEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.entries) == 0 {
		return nil
	}
	e := l.entries[len(l.entries)-1]
	return &e
}

// Clear removes all entries.
func (l *CommandLog) Clear() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = l.entries[:0]
	l.unreadErrors = 0
}

// UnreadErrors returns the number of failed commands logged since the last
// MarkRead. Use to surface a "something failed, check the console" hint.
func (l *CommandLog) UnreadErrors() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.unreadErrors
}

// MarkRead resets the unread-errors counter. Call when the user has had a
// chance to see the failures (e.g. focused the Console panel).
func (l *CommandLog) MarkRead() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.unreadErrors = 0
}

// Subscribe returns a channel that receives new console entries.
func (l *CommandLog) Subscribe() chan ConsoleEntry {
	ch := make(chan ConsoleEntry, 8)
	l.mu.Lock()
	l.subscribers[ch] = struct{}{}
	l.mu.Unlock()
	return ch
}

// Unsubscribe removes a subscriber channel.
func (l *CommandLog) Unsubscribe(ch chan ConsoleEntry) {
	l.mu.Lock()
	delete(l.subscribers, ch)
	l.mu.Unlock()
	close(ch)
}

func splitLines(s string) []string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	return lines
}

func isWarnLine(line string) bool {
	lower := strings.ToLower(line)
	for _, p := range warnPatterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}
