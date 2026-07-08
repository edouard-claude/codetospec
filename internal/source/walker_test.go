package source

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWalkExcludesDirsAndGlobs(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{
		"index.php",
		"docs/analyse.md",
		"notes.md",
		"data/export.csv",
		"vendor/lib.php",
		"usine/accueil.php",
	} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("<?php echo 1;\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	files, err := Walk(root, []string{"vendor", "*.md", "*.csv"})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	got := make(map[string]bool, len(files))
	for _, f := range files {
		got[f.Path] = true
	}
	if !got["index.php"] || !got["usine/accueil.php"] {
		t.Errorf("expected php files kept, got %v", got)
	}
	for _, banned := range []string{"notes.md", "docs/analyse.md", "data/export.csv", "vendor/lib.php"} {
		if got[banned] {
			t.Errorf("%s should be excluded", banned)
		}
	}
}
