package cvs

import "strings"

// Module is one entry from the curated CVSROOT/modules admin file
// (as printed by `cvs -d ROOT co -c`).
//
// The display name and the on-disk checkout directory can differ when
// the entry uses `-d alias`:
//
//	mymodule -d MyDirName real/repo/path
//
// Picks `mymodule` for the picker but checks out into `MyDirName/`.
type Module struct {
	Name        string // first whitespace-separated token, shown to the user
	CheckoutDir string // -d alias if present, otherwise Name
	Definition  string // the remainder of the line (everything after Name)
}

// ParseModules turns `cvs co -c` output into a Module slice.
//
// Format: one module per line. The first non-comment, non-empty token is
// the module name; an optional `-d <alias>` later on the line overrides
// the on-disk directory name. Lines beginning with `#` (after optional
// leading whitespace) and blank lines are skipped. Lines that don't have
// a name token are silently dropped — `co -c` itself never emits them.
func ParseModules(output string) []Module {
	var mods []Module
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) == 0 {
			continue
		}
		name := fields[0]
		checkoutDir := name
		// Walk the rest looking for `-d <alias>`. The alias is the very
		// next token; bail out at the last index to keep the lookahead
		// in-bounds.
		for i := 1; i < len(fields)-1; i++ {
			if fields[i] == "-d" {
				checkoutDir = fields[i+1]
				break
			}
		}
		definition := ""
		if len(fields) > 1 {
			definition = strings.Join(fields[1:], " ")
		}
		mods = append(mods, Module{
			Name:        name,
			CheckoutDir: checkoutDir,
			Definition:  definition,
		})
	}
	return mods
}
