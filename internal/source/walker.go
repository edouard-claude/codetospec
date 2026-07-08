// Package source walks the analyzed repository and detects languages by
// file extension.
package source

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"codetospec/internal/sitter"
)

// File is one source file discovered under the repository root.
type File struct {
	Path     string // slash-separated, relative to the root
	AbsPath  string
	Language string // "" when no grammar matches the extension
}

// Walk lists source files under root in deterministic (lexical) order,
// skipping excluded entries, hidden entries and binary files. An exclude
// entry matches directory and file names exactly or as a glob pattern
// (e.g. "*.md", "*.csv").
func Walk(root string, exclude []string) ([]File, error) {
	var patterns []string
	for _, name := range exclude {
		if name = strings.TrimSpace(name); name != "" {
			patterns = append(patterns, name)
		}
	}
	excluded := func(name string) bool {
		for _, p := range patterns {
			if p == name {
				return true
			}
			if ok, err := filepath.Match(p, name); err == nil && ok {
				return true
			}
		}
		return false
	}

	var files []File
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if path == root {
				return nil
			}
			if excluded(name) || strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(name, ".") || excluded(name) {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("relativize %s: %w", path, err)
		}
		if isBinary(path) {
			return nil
		}
		files = append(files, File{
			Path:     filepath.ToSlash(rel),
			AbsPath:  path,
			Language: sitter.LanguageForPath(name),
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk %s: %w", root, err)
	}
	return files, nil
}

// isBinary sniffs the first bytes of a file for a NUL byte.
func isBinary(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return true
	}
	defer f.Close()
	buf := make([]byte, 8000)
	n, _ := f.Read(buf)
	return bytes.IndexByte(buf[:n], 0) >= 0
}
