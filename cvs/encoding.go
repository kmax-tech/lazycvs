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

// ExpectedEncoding is the text encoding a repository's files are
// supposed to use. Classic CVS repos predate UTF-8, so Latin-1 is the
// default expectation; it only steers the warning classification, never
// how bytes are read, written, or committed.
type ExpectedEncoding int

const (
	ExpectLatin1 ExpectedEncoding = iota // zero value — the CVS default
	ExpectUTF8
)

// ParseExpectedEncoding maps the config string to an ExpectedEncoding.
// Anything but an explicit "utf8"/"utf-8" (unset, "latin1", typos)
// falls back to the Latin-1 default.
func ParseExpectedEncoding(s string) ExpectedEncoding {
	switch s {
	case "utf8", "utf-8", "UTF8", "UTF-8":
		return ExpectUTF8
	default:
		return ExpectLatin1
	}
}

// EncodingIssue classifies encoding problems in text file content.
type EncodingIssue int

const (
	EncodingOK             EncodingIssue = iota
	EncodingUnexpectedUTF8               // multi-byte UTF-8 in a Latin-1 repo
	EncodingMojibake                     // double-encoded UTF-8 patterns present
	EncodingNonISO                       // C1 bytes 0x80-0x9F: CP1252, not ISO Latin-1
	EncodingNotUTF8                      // invalid UTF-8 in a UTF-8 repo (Latin-1?)
)

func (e EncodingIssue) String() string {
	switch e {
	case EncodingUnexpectedUTF8:
		return "UTF-8 (expected Latin-1)"
	case EncodingMojibake:
		return "mojibake (double-encoded UTF-8?)"
	case EncodingNonISO:
		return "non-ISO bytes (CP1252?)"
	case EncodingNotUTF8:
		return "not UTF-8 (Latin-1?)"
	default:
		return ""
	}
}

// mojibakeThreshold is the number of pattern hits required for a Mojibake
// verdict. 1 catches the common real case (a small file with a single
// broken umlaut, "MÃ¼ller"); the follower-set restriction below already
// excludes legitimate Ã/Â uses like "NÃO"/"SÃO", whose followers are
// letters.
const mojibakeThreshold = 1

// cp1252High holds the CP1252 interpretations of bytes 0x80-0x9F. When
// CP1252 text is mistakenly decoded as Latin-1 and re-encoded as UTF-8,
// these runes appear as the second character of a mojibake pair (e.g.
// "Ÿ" in "GrÃ¶ÃŸe").
var cp1252High = map[rune]bool{
	'€': true, '‚': true, 'ƒ': true, '„': true,
	'…': true, '†': true, '‡': true, 'ˆ': true,
	'‰': true, 'Š': true, '‹': true, 'Œ': true,
	'Ž': true, '‘': true, '’': true, '“': true,
	'”': true, '•': true, '–': true, '—': true,
	'˜': true, '™': true, 'š': true, '›': true,
	'œ': true, 'ž': true, 'Ÿ': true,
}

// isMojibakeFollower reports whether r is a plausible second character of
// a double-encoded UTF-8 pair: the Latin-1/CP1252 reading of a UTF-8
// continuation byte (0x80-0xBF). Letters are deliberately not in the set,
// which keeps words like "NÃO" or "château" clean.
func isMojibakeFollower(r rune) bool {
	return (r >= 0x80 && r <= 0xBF) || cp1252High[r]
}

// DetectEncodingIssue inspects raw text bytes for encoding problems
// relative to the repo's expected encoding. Callers must gate out
// binary files themselves (fs.IsBinary) — NUL bytes are valid UTF-8
// and would be reported as OK here. The input may be a capped prefix
// of the file; a rune split at the buffer end is tolerated.
//
// With ExpectLatin1 (the CVS default): pure ASCII and clean Latin-1
// (high bytes 0xA0-0xFF) are OK; C1 bytes 0x80-0x9F flag as NonISO
// (CP1252 smart quotes and friends aren't ISO-8859-1 characters);
// multi-byte UTF-8 content flags as UnexpectedUTF8 — a Latin-1
// consumer renders it as mojibake — or as Mojibake when it carries
// double-encoding patterns.
//
// With ExpectUTF8: invalid UTF-8 flags as NotUTF8, double-encoding
// patterns as Mojibake.
func DetectEncodingIssue(data []byte, expect ExpectedEncoding) EncodingIssue {
	data = trimPartialRune(data)
	if expect == ExpectUTF8 {
		if !utf8.Valid(data) {
			return EncodingNotUTF8
		}
		if hasMojibakePattern(data) {
			return EncodingMojibake
		}
		return EncodingOK
	}
	// Latin-1 expected.
	if utf8.Valid(data) {
		multiByte := false
		for _, b := range data {
			if b >= 0x80 {
				multiByte = true
				break
			}
		}
		if !multiByte {
			return EncodingOK // pure ASCII is a subset of Latin-1
		}
		if hasMojibakePattern(data) {
			return EncodingMojibake
		}
		return EncodingUnexpectedUTF8
	}
	// Invalid UTF-8 — read the high bytes as Latin-1. 0xA0-0xFF are
	// proper ISO-8859-1 characters; 0x80-0x9F are control codes there
	// and almost certainly CP1252 punctuation.
	for _, b := range data {
		if b >= 0x80 && b <= 0x9F {
			return EncodingNonISO
		}
	}
	return EncodingOK
}

// hasMojibakePattern reports whether valid UTF-8 data contains
// double-encoding artifacts.
func hasMojibakePattern(data []byte) bool {
	hits := 0
	var prev1, prev2 rune
	for i := 0; i < len(data); {
		r, size := utf8.DecodeRune(data[i:])
		i += size
		switch {
		case (prev1 == 'Ã' || prev1 == 'Â') && isMojibakeFollower(r):
			// 'Ã'/'Â' + continuation reading: double-encoded 2-byte rune.
			hits++
		case prev2 == 'â' && isMojibakeFollower(prev1) && isMojibakeFollower(r):
			// 'â' + two continuation readings: double-encoded 3-byte rune
			// (e.g. an em-dash). Requiring two followers keeps French
			// words like "âge" clean.
			hits++
		}
		if hits >= mojibakeThreshold {
			return true
		}
		prev2, prev1 = prev1, r
	}
	return false
}

// trimPartialRune drops a multi-byte rune that was split by a capped
// read at the end of data. Genuinely invalid tails (complete sequences
// that fail validation, lone Latin-1 high bytes) are kept so they still
// produce a NotUTF8 verdict.
func trimPartialRune(data []byte) []byte {
	for i := 1; i <= utf8.UTFMax && i <= len(data); i++ {
		b := data[len(data)-i]
		if b&0xC0 == 0x80 {
			continue // continuation byte, keep scanning back
		}
		if b >= 0xC0 && expectedRuneLen(b) > i && !utf8.Valid(data[len(data)-i:]) {
			return data[:len(data)-i]
		}
		return data
	}
	return data
}

func expectedRuneLen(b byte) int {
	switch {
	case b&0xE0 == 0xC0:
		return 2
	case b&0xF0 == 0xE0:
		return 3
	case b&0xF8 == 0xF0:
		return 4
	}
	return 1
}
