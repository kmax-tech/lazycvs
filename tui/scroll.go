package tui

// ensureCursorVisible computes the new viewport offset so that cursor
// stays inside the visible window of size visibleRows. Each list-view
// model owns its own translation from panel height to visibleRows
// (e.g. staged subtracts a header line, history divides by 2 because
// each entry takes two rows), then delegates the offset math here.
func ensureCursorVisible(cursor, offset, visibleRows int) int {
	if visibleRows < 1 {
		visibleRows = 1
	}
	if cursor < offset {
		return cursor
	}
	if cursor >= offset+visibleRows {
		return cursor - visibleRows + 1
	}
	return offset
}
