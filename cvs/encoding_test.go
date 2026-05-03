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
