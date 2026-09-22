package cvs

import "testing"

func TestEnsureUTF8(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "valid utf8 passes through",
			in:   "Im Rahmen von Arbeitspaket A1 wurde untersucht",
			want: "Im Rahmen von Arbeitspaket A1 wurde untersucht",
		},
		{
			name: "valid utf8 with multi-byte chars passes through",
			in:   "für 20 Themen", // "für 20 Themen" as UTF-8
			want: "für 20 Themen",
		},
		{
			name: "latin-1 umlaut transcodes",
			// "für" in Latin-1: 'f' (0x66), 'ü' (0xFC), 'r' (0x72)
			in:   "f\xfcr 20 Themen",
			want: "für 20 Themen",
		},
		{
			name: "latin-1 multiple umlauts",
			// "Größe für" = G(0x47) r(0x72) ö(0xF6) ß(0xDF) e(0x65) ' '(0x20) f(0x66) ü(0xFC) r(0x72)
			in:   "Gr\xf6\xdfe f\xfcr",
			want: "Größe für",
		},
		{
			name: "empty string",
			in:   "",
			want: "",
		},
		{
			name: "ascii only",
			in:   "hello world",
			want: "hello world",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EnsureUTF8(tt.in); got != tt.want {
				t.Errorf("EnsureUTF8(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestDetectEncodingIssueLatin1Repo(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
		want EncodingIssue
	}{
		{
			name: "ascii is ok",
			in:   []byte("hello world"),
			want: EncodingOK,
		},
		{
			name: "empty is ok",
			in:   []byte{},
			want: EncodingOK,
		},
		{
			name: "clean latin-1 is ok — it is the repo standard",
			// "Größe für" in Latin-1: ö=0xF6, ß=0xDF, ü=0xFC
			in:   []byte("Gr\xf6\xdfe f\xfcr 20 Themen"),
			want: EncodingOK,
		},
		{
			name: "utf8 umlauts flag as unexpected utf8",
			in:   []byte("Größe für 20 Themen"),
			want: EncodingUnexpectedUTF8,
		},
		{
			name: "utf8 portuguese flags as unexpected utf8",
			in:   []byte("NÃO SÃO JOÃO"),
			want: EncodingUnexpectedUTF8,
		},
		{
			name: "cp1252 smart quotes flag as non-iso",
			in:   []byte("\x93quote\x94 f\xfcr"),
			want: EncodingNonISO,
		},
		{
			name: "cp1252 em-dash flags as non-iso",
			// 0x97 is — in CP1252, a control code in ISO-8859-1; the
			// invalid-UTF-8 0xFC keeps this out of the valid-UTF-8 branch
			in:   []byte("a \x97 f\xfcr b"),
			want: EncodingNonISO,
		},
		{
			name: "double-encoded umlaut is mojibake",
			// "Müller" double-encoded: ü -> Ã¼
			in:   []byte("MÃ¼ller"),
			want: EncodingMojibake,
		},
		{
			name: "double-encoded cp1252 follower is mojibake",
			// "Größe" double-encoded via CP1252: ß -> ÃŸ (Ÿ is CP1252 0x9F)
			in:   []byte("GrÃ¶ÃŸe"),
			want: EncodingMojibake,
		},
		{
			name: "double-encoded middle dot is mojibake",
			// "·" double-encoded: Â·
			in:   []byte("Â· item Â· item"),
			want: EncodingMojibake,
		},
		{
			name: "double-encoded em-dash is mojibake",
			// "—" (U+2014, bytes E2 80 94) double-encoded via CP1252:
			// 0xE2 -> "â", 0x80 -> "€", 0x94 -> "”"
			in:   []byte("one â€” two"),
			want: EncodingMojibake,
		},
		{
			name: "utf8 rune split by capped read counts as utf8",
			// "aü" cut after the first byte of ü: the partial rune is
			// trimmed, leaving pure ASCII
			in:   []byte("aü")[:2],
			want: EncodingOK,
		},
		{
			name: "nul bytes are ok — callers must gate binaries",
			in:   []byte("PK\x00\x00data"),
			want: EncodingOK,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DetectEncodingIssue(tt.in, ExpectLatin1); got != tt.want {
				t.Errorf("DetectEncodingIssue(%q, ExpectLatin1) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestDetectEncodingIssueUTF8Repo(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
		want EncodingIssue
	}{
		{
			name: "valid utf8 german is ok",
			in:   []byte("Größe für 20 Themen"),
			want: EncodingOK,
		},
		{
			name: "latin-1 umlaut is not utf8",
			in:   []byte("f\xfcr 20 Themen"),
			want: EncodingNotUTF8,
		},
		{
			name: "double-encoded umlaut is mojibake",
			in:   []byte("MÃ¼ller"),
			want: EncodingMojibake,
		},
		{
			name: "legit portuguese caps are ok",
			in:   []byte("NÃO SÃO JOÃO"),
			want: EncodingOK,
		},
		{
			name: "legit french accents are ok",
			in:   []byte("Âge d'or, château, âme, não sério"),
			want: EncodingOK,
		},
		{
			name: "rune split by capped read is ok",
			in:   []byte("aü")[:2],
			want: EncodingOK,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DetectEncodingIssue(tt.in, ExpectUTF8); got != tt.want {
				t.Errorf("DetectEncodingIssue(%q, ExpectUTF8) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseExpectedEncoding(t *testing.T) {
	for s, want := range map[string]ExpectedEncoding{
		"":       ExpectLatin1,
		"latin1": ExpectLatin1,
		"bogus":  ExpectLatin1,
		"utf8":   ExpectUTF8,
		"utf-8":  ExpectUTF8,
		"UTF-8":  ExpectUTF8,
	} {
		if got := ParseExpectedEncoding(s); got != want {
			t.Errorf("ParseExpectedEncoding(%q) = %v, want %v", s, got, want)
		}
	}
}
