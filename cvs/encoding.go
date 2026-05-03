package cvs

import "unicode/utf8"

// EnsureUTF8 returns s unchanged if it is already valid UTF-8, otherwise
// treats the bytes as Latin-1 (ISO-8859-1) and transcodes them to UTF-8.
//
// CVS itself is encoding-agnostic — it stores and emits raw bytes from
// whatever encoding the source files use. Many older repos hold files in
// Latin-1, which collides with Go's UTF-8 string semantics: bytes like
// 0xFC ("ü") are invalid UTF-8 byte sequences, and lipgloss / x/ansi
// then miscount cell widths. Wrong width counts mean wrong padding and
// truncation in renderPanel, which lets content overflow the panel and
// the terminal then wraps it — producing visible glitches like
// "untersuch��" and shifted neighboring panels.
//
// The Latin-1 fallback maps each byte 0x00-0xFF directly to U+0000-U+00FF;
// Go's `string([]rune)` then re-encodes those code points as proper
// UTF-8. This is lossless for any genuinely Latin-1 input.
func EnsureUTF8(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	runes := make([]rune, len(s))
	for i, b := range []byte(s) {
		runes[i] = rune(b)
	}
	return string(runes)
}
