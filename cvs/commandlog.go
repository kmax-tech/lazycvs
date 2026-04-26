package cvs

import (
	"strings"
	"sync"
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

	// Classify stdout lines
	if result.Stdout != "" {
		entry.StdoutLines = splitLines(result.Stdout)
	}

	// Classify stderr lines into warnings vs errors
	if result.Stderr != "" {
		for _, line := range splitLines(result.Stderr) {
			if isWarnLine(line) {
				entry.WarnLines = append(entry.WarnLines, line)
			} else {
				entry.StderrLines = append(entry.StderrLines, line)
			}
		}
	}

	l.mu.Lock()
	if len(l.entries) >= l.maxSize {
		l.entries = l.entries[1:]
	}
	l.entries = append(l.entries, entry)
	if !entry.Success {
		l.unreadErrors++
	}

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
