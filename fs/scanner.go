package fs

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var binaryExtensions = map[string]bool{
	".pdf":   true,
	".png":   true,
	".jpg":   true,
	".jpeg":  true,
	".gif":   true,
	".bmp":   true,
	".tiff":  true,
	".ico":   true,
	".webp":  true,
	".zip":   true,
	".tar":   true,
	".gz":    true,
	".bz2":   true,
	".xz":    true,
	".7z":    true,
	".rar":   true,
	".doc":   true,
	".docx":  true,
	".xls":   true,
	".xlsx":  true,
	".ppt":   true,
	".pptx":  true,
	".odt":   true,
	".ods":   true,
	".ai":    true,
	".psd":   true,
	".eps":   true,
	".exe":   true,
	".dll":   true,
	".so":    true,
	".dylib": true,
	".o":     true,
	".a":     true,
	".class": true,
	".jar":   true,
	".war":   true,
	".mp3":   true,
	".mp4":   true,
	".avi":   true,
	".mov":   true,
	".wav":   true,
	".flac":  true,
	".ogg":   true,
	".ttf":   true,
	".otf":   true,
	".woff":  true,
	".woff2": true,
	".db":    true,
	".sqlite": true,
}

var textExtensions = map[string]bool{
	".go":         true,
	".mod":        true,
	".sum":        true,
	".c":          true,
	".h":          true,
	".cc":         true,
	".cpp":        true,
	".cxx":        true,
	".hpp":        true,
	".rs":         true,
	".py":         true,
	".rb":         true,
	".pl":         true,
	".lua":        true,
	".js":         true,
	".mjs":        true,
	".cjs":        true,
	".ts":         true,
	".tsx":        true,
	".jsx":        true,
	".html":       true,
	".htm":        true,
	".xml":        true,
	".svg":        true,
	".css":        true,
	".scss":       true,
	".sass":       true,
	".less":       true,
	".json":       true,
	".yaml":       true,
	".yml":        true,
	".toml":       true,
	".ini":        true,
	".cfg":        true,
	".conf":       true,
	".md":         true,
	".markdown":   true,
	".rst":        true,
	".txt":        true,
	".log":        true,
	".csv":        true,
	".tsv":        true,
	".sh":         true,
	".bash":       true,
	".zsh":        true,
	".fish":       true,
	".ps1":        true,
	".bat":        true,
	".cmd":        true,
	".tex":        true,
	".bib":        true,
	".diff":       true,
	".patch":      true,
	".gitignore":  true,
	".gitattributes": true,
	".dockerfile": true,
	".env":        true,
	".java":       true,
	".kt":         true,
	".scala":      true,
	".swift":      true,
	".m":          true,
	".php":        true,
	".sql":        true,
	".vue":        true,
	".svelte":     true,
	".elm":        true,
	".clj":        true,
	".ex":         true,
	".exs":        true,
	".erl":        true,
	".hs":         true,
	".ml":         true,
	".mli":        true,
	".r":          true,
	".jl":         true,
}

// Filenames that are conventionally text even with no extension.
var textFilenames = map[string]bool{
	"makefile":      true,
	"dockerfile":    true,
	"readme":        true,
	"license":       true,
	"copying":       true,
	"changelog":     true,
	"authors":       true,
	"contributors":  true,
	"todo":          true,
	"notice":        true,
	"install":       true,
	"news":          true,
	".cvsignore":    true,
	".gitignore":    true,
	".dockerignore": true,
	".editorconfig": true,
}

// IsBinary reports whether the file at absPath is binary.
//
// It checks in order:
//  1. Known binary extension (e.g. .png, .zip) -> binary
//  2. Known text extension (e.g. .go, .md) or known text filename (Makefile) -> text
//  3. Fall back to sniffing the first 512 bytes: a NUL byte means binary.
//
// On read errors (e.g. file does not exist), the unknown case is treated as text
// so the caller can surface the real error from its own subsequent file access.
func IsBinary(absPath string) bool {
	ext := strings.ToLower(filepath.Ext(absPath))
	if binaryExtensions[ext] {
		return true
	}
	if textExtensions[ext] {
		return false
	}
	if ext == "" {
		base := strings.ToLower(filepath.Base(absPath))
		if textFilenames[base] {
			return false
		}
	}
	return sniffBinary(absPath)
}

// sniffBinary reads the first 512 bytes of the file and returns true if a NUL
// byte is found. Any read error is treated as "not binary" — the caller will
// hit the same error when it tries to open the file itself.
func sniffBinary(absPath string) bool {
	f, err := os.Open(absPath)
	if err != nil {
		return false
	}
	defer f.Close()

	var buf [512]byte
	n, err := f.Read(buf[:])
	if err != nil && err != io.EOF {
		return false
	}
	return bytes.IndexByte(buf[:n], 0) >= 0
}
