package config

// Config is the top-level configuration for lazycvs.
type Config struct {
	CVS       CVSConfig       `toml:"cvs" json:"cvs"`
	Favorites FavoritesConfig `toml:"favorites" json:"favorites"`
	Ignore    IgnoreConfig    `toml:"ignore" json:"ignore"`
	Editor    EditorConfig    `toml:"editor" json:"editor"`
}

// CVSConfig holds CVS binary and execution settings.
type CVSConfig struct {
	Binary       string   `toml:"binary" json:"binary"`
	DefaultFlags []string `toml:"default_flags" json:"default_flags"`
	Timeout      int      `toml:"timeout" json:"timeout"`
	// DefaultPath is the working-copy directory to fall back to when the user
	// runs lazycvs without a path argument and the current working directory
	// has no CVS metadata. Supports a leading "~" for the home directory.
	DefaultPath string `toml:"default_path,omitempty" json:"default_path,omitempty"`
	// Root is the default CVSROOT for `lazycvs init` when $CVSROOT is unset.
	// e.g. ":pserver:user@host:/srv/cvsroot" or ":ext:user@host:/srv/cvsroot".
	Root string `toml:"root,omitempty" json:"root,omitempty"`
	// SSHKey, when set, makes `lazycvs init` export
	// CVS_RSH=ssh -i <SSHKey>` so :ext: connections use a specific key.
	// CVS interpolates CVS_RSH shell-style, so the path must NOT contain
	// whitespace — set CVS_RSH yourself for paths with spaces.
	SSHKey string `toml:"ssh_key,omitempty" json:"ssh_key,omitempty"`
}

// FavoritesConfig holds the list of favorite directories.
type FavoritesConfig struct {
	Dirs []FavoriteDir `toml:"dirs" json:"dirs"`
}

// FavoriteDir is a single favorited directory.
type FavoriteDir struct {
	Name  string `toml:"name" json:"name"`
	Path  string `toml:"path" json:"path"`
	Color string `toml:"color,omitempty" json:"color,omitempty"`
}

// IgnoreConfig holds ignore pattern settings.
type IgnoreConfig struct {
	GlobalPatterns []string `toml:"global_patterns" json:"global_patterns"`
	SyncCvsignore  bool     `toml:"sync_cvsignore" json:"sync_cvsignore"`
}

// EditorConfig holds external editor settings.
type EditorConfig struct {
	// 2-way diff tool — invoked by `E` (file vs HEAD, rev vs rev, etc.).
	DiffTool    string `toml:"diff_tool,omitempty" json:"diff_tool,omitempty"`
	DiffCommand string `toml:"diff_command,omitempty" json:"diff_command,omitempty"`
	// 3-way merge tool — invoked by `M` on conflict (`C`-status) files.
	// Templates can use $BASE (common ancestor), $LOCAL (your pre-merge
	// working copy), $REMOTE (the HEAD that conflicted), $MERGED (the on-disk
	// working file with conflict markers — most tools save back to here).
	MergeTool    string `toml:"merge_tool,omitempty" json:"merge_tool,omitempty"`
	MergeCommand string `toml:"merge_command,omitempty" json:"merge_command,omitempty"`
}
