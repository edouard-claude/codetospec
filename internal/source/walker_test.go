package source

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLooksBinary(t *testing.T) {
	// A SCIP protobuf index opens with ASCII tool/path strings (no early NUL)
	// then invalid-UTF-8 varint bytes — the case the NUL-only sniff missed.
	scip := append([]byte("\n7\x08\x01\x12$\n\x08scip-php\x12\x050.0.1\x1a\x11--memory-limit=2G"),
		0xb1, 0xc3, 0x28, 0xff, 0xfe, 0x93, 0x12)
	cases := []struct {
		name string
		buf  []byte
		want bool
	}{
		{"empty", nil, false},
		{"php source", []byte("<?php\nfunction f() { return 1; }\n"), false},
		{"utf8 accents", []byte("// règle métier : activation prorata\n"), false},
		{"nul byte", []byte("abc\x00def"), true},
		{"scip protobuf", scip, true},
	}
	for _, c := range cases {
		if got := looksBinary(c.buf); got != c.want {
			t.Errorf("looksBinary(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestWalkSkipsBinaryFiles(t *testing.T) {
	root := t.TempDir()
	write := func(rel string, data []byte) {
		if err := os.WriteFile(filepath.Join(root, rel), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("app.php", []byte("<?php echo 1;\n"))
	// index.scip: protobuf header, ASCII strings, then invalid-UTF-8 bytes.
	write("index.scip", append([]byte("\n7\x08\x01\x12$\n\x08scip-php\x12\x050.0.1"),
		0xb1, 0xc3, 0x28, 0xff, 0x93))

	files, err := Walk(root, nil)
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	got := map[string]bool{}
	for _, f := range files {
		got[f.Path] = true
	}
	if !got["app.php"] {
		t.Error("app.php should be kept")
	}
	if got["index.scip"] {
		t.Error("index.scip is a binary protobuf and must not be chunked")
	}
}

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
