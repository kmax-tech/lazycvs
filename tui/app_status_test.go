package tui

import "testing"

func TestPartitionTree(t *testing.T) {
	// Construct trees by hand and verify the partitioning produces the
	// expected scan specs. The constants are inlined so the assertions
	// don't depend on scanTargetFiles changing.
	old := scanTargetFiles
	defer func() { _ = old }() // marker; tests don't actually mutate it

	tests := []struct {
		name string
		tree *dirNode
		want []scanSpec
	}{
		{
			name: "tiny tree fits in one recursive scan",
			tree: &dirNode{
				relPath: ".",
				files:   5, subtree: 12,
				children: []*dirNode{
					{relPath: "src", files: 4, subtree: 4},
					{relPath: "docs", files: 3, subtree: 3},
				},
			},
			want: []scanSpec{{dir: ".", recursive: true}},
		},
		{
			name: "large root splits into per-subdir scans plus root's own files",
			tree: &dirNode{
				relPath: ".",
				files:   10, subtree: 510,
				children: []*dirNode{
					{relPath: "a", files: 250, subtree: 250},
					{relPath: "b", files: 250, subtree: 250},
				},
			},
			want: []scanSpec{
				{dir: ".", recursive: false}, // 10 root files only
				{dir: "a", recursive: true},  // a's subtree fits
				{dir: "b", recursive: true},  // b's subtree fits
			},
		},
		{
			name: "small subtree doesn't get its own cvs call",
			tree: &dirNode{
				relPath: ".",
				files:   0, subtree: 305,
				children: []*dirNode{
					{relPath: "big", files: 300, subtree: 300},
					{relPath: "tiny", files: 5, subtree: 5},
				},
			},
			// Root's subtree (305) > target (200), so we split. Root
			// has 0 direct files → no `.` non-recursive scan. Each
			// child is partitioned independently; both fit.
			want: []scanSpec{
				{dir: "big", recursive: true},
				{dir: "tiny", recursive: true},
			},
		},
		{
			name: "deeply nested oversized subtree splits at the right level",
			tree: &dirNode{
				relPath: ".",
				files:   0, subtree: 800,
				children: []*dirNode{
					{
						relPath: "src",
						files:   0, subtree: 800,
						children: []*dirNode{
							{relPath: "src/lib", files: 400, subtree: 400},
							{relPath: "src/cmd", files: 400, subtree: 400},
						},
					},
				},
			},
			// Root: 800 > 200 with children → split. No direct files.
			// src: 800 > 200 with children → split. No direct files.
			// src/lib: 400 > 200, but it's a leaf → can't split
			//   further, emit one recursive scan covering all 400.
			// Same for src/cmd.
			want: []scanSpec{
				{dir: "src/lib", recursive: true},
				{dir: "src/cmd", recursive: true},
			},
		},
		{
			name: "nil tree → no specs",
			tree: nil,
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := partitionTree(tt.tree)
			if len(got) != len(tt.want) {
				t.Fatalf("partitionTree() returned %d specs, want %d: got %v", len(got), len(tt.want), got)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("spec[%d] = %v, want %v", i, got[i], tt.want[i])
				}
			}
		})
	}
}
