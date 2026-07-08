// Package sitter wraps tree-sitter behind a language-neutral API: a grammar
// registry, per-language .scm queries (definitions, imports, modules) and
// AST-based chunking. Adding a language means adding a grammar entry and a
// query file; the rest of the program never mentions a specific language.
package sitter

import (
	"embed"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_go "github.com/tree-sitter/tree-sitter-go/bindings/go"
	tree_sitter_javascript "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	tree_sitter_php "github.com/tree-sitter/tree-sitter-php/bindings/go"
	tree_sitter_rust "github.com/tree-sitter/tree-sitter-rust/bindings/go"
	tree_sitter_typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

//go:embed queries/*.scm
var queryFS embed.FS

// Definition is a named code definition located by the language query.
type Definition struct {
	Kind       string // "class", "function", "method", "interface", ...
	Name       string
	Visibility string // empty when the language/query does not provide it
	StartLine  int    // 1-based
	EndLine    int    // 1-based inclusive
}

// Import is one dependency statement located by the language query.
type Import struct {
	Target    string
	StartLine int
	EndLine   int
}

// Module is one namespace/package unit declared in a file.
type Module struct {
	Name      string
	StartLine int
	EndLine   int
}

// FileInfo is everything the universal layer extracts from one file.
type FileInfo struct {
	Language    string
	Definitions []Definition
	Imports     []Import
	Modules     []Module
}

// grammar couples a tree-sitter language constructor with its query file.
type grammar struct {
	name      string
	queryFile string
	language  func() *tree_sitter.Language

	once  sync.Once
	lang  *tree_sitter.Language
	query *tree_sitter.Query
	err   error
}

var grammars = map[string]*grammar{
	"php": {name: "php", queryFile: "queries/php.scm", language: func() *tree_sitter.Language {
		return tree_sitter.NewLanguage(tree_sitter_php.LanguagePHP())
	}},
	"go": {name: "go", queryFile: "queries/go.scm", language: func() *tree_sitter.Language {
		return tree_sitter.NewLanguage(tree_sitter_go.Language())
	}},
	"javascript": {name: "javascript", queryFile: "queries/javascript.scm", language: func() *tree_sitter.Language {
		return tree_sitter.NewLanguage(tree_sitter_javascript.Language())
	}},
	"typescript": {name: "typescript", queryFile: "queries/typescript.scm", language: func() *tree_sitter.Language {
		return tree_sitter.NewLanguage(tree_sitter_typescript.LanguageTypescript())
	}},
	"tsx": {name: "tsx", queryFile: "queries/tsx.scm", language: func() *tree_sitter.Language {
		return tree_sitter.NewLanguage(tree_sitter_typescript.LanguageTSX())
	}},
	"rust": {name: "rust", queryFile: "queries/rust.scm", language: func() *tree_sitter.Language {
		return tree_sitter.NewLanguage(tree_sitter_rust.Language())
	}},
}

var extensions = map[string]string{
	".php": "php",
	".go":  "go",
	".js":  "javascript",
	".jsx": "javascript",
	".mjs": "javascript",
	".cjs": "javascript",
	".ts":  "typescript",
	".tsx": "tsx",
	".rs":  "rust",
}

// LanguageForPath returns the registered language for a file path, or ""
// when no grammar matches its extension.
func LanguageForPath(path string) string {
	return extensions[strings.ToLower(filepath.Ext(path))]
}

func (g *grammar) compile() (*tree_sitter.Language, *tree_sitter.Query, error) {
	g.once.Do(func() {
		src, err := queryFS.ReadFile(g.queryFile)
		if err != nil {
			g.err = fmt.Errorf("read query %s: %w", g.queryFile, err)
			return
		}
		g.lang = g.language()
		query, queryErr := tree_sitter.NewQuery(g.lang, string(src))
		if queryErr != nil {
			g.err = fmt.Errorf("compile query %s: %w", g.queryFile, queryErr)
			return
		}
		g.query = query
	})
	return g.lang, g.query, g.err
}

// Parse extracts definitions, imports and modules from one source file.
func Parse(language string, content []byte) (*FileInfo, error) {
	g, ok := grammars[language]
	if !ok {
		return nil, fmt.Errorf("no grammar registered for language %q", language)
	}
	lang, query, err := g.compile()
	if err != nil {
		return nil, err
	}

	parser := tree_sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(lang); err != nil {
		return nil, fmt.Errorf("set language %s: %w", language, err)
	}
	tree := parser.Parse(content, nil)
	if tree == nil {
		return nil, fmt.Errorf("tree-sitter returned no tree for language %s", language)
	}
	defer tree.Close()

	cursor := tree_sitter.NewQueryCursor()
	defer cursor.Close()
	matches := cursor.Matches(query, tree.RootNode(), content)
	names := query.CaptureNames()

	info := &FileInfo{Language: language}
	for m := matches.Next(); m != nil; m = matches.Next() {
		var def, name, visibility, module, moduleName, imp, impTarget *tree_sitter.Node
		defKind := ""
		for i := range m.Captures {
			c := &m.Captures[i]
			switch captureName := names[c.Index]; {
			case strings.HasPrefix(captureName, "def."):
				def, defKind = &c.Node, strings.TrimPrefix(captureName, "def.")
			case captureName == "name":
				name = &c.Node
			case captureName == "visibility":
				visibility = &c.Node
			case captureName == "module":
				module = &c.Node
			case captureName == "module.name":
				moduleName = &c.Node
			case captureName == "import":
				imp = &c.Node
			case captureName == "import.target":
				impTarget = &c.Node
			}
		}
		switch {
		case def != nil && name != nil:
			d := Definition{
				Kind:      defKind,
				Name:      name.Utf8Text(content),
				StartLine: startLine(def),
				EndLine:   endLine(def),
			}
			if visibility != nil {
				d.Visibility = visibility.Utf8Text(content)
			}
			info.Definitions = append(info.Definitions, d)
		case module != nil && moduleName != nil:
			info.Modules = append(info.Modules, Module{
				Name:      moduleName.Utf8Text(content),
				StartLine: startLine(module),
				EndLine:   endLine(module),
			})
		case imp != nil || impTarget != nil:
			span := imp
			if span == nil {
				span = impTarget
			}
			target := span.Utf8Text(content)
			if impTarget != nil {
				target = impTarget.Utf8Text(content)
			}
			info.Imports = append(info.Imports, Import{
				Target:    cleanImportTarget(target),
				StartLine: startLine(span),
				EndLine:   endLine(span),
			})
		}
	}
	dedupInfo(info)
	return info, nil
}

func startLine(n *tree_sitter.Node) int {
	return int(n.StartPosition().Row) + 1
}

// endLine converts an exclusive end position into a 1-based inclusive line.
func endLine(n *tree_sitter.Node) int {
	pos := n.EndPosition()
	if pos.Column == 0 && pos.Row > 0 {
		return int(pos.Row)
	}
	return int(pos.Row) + 1
}

// cleanImportTarget strips quotes, trailing separators and language noise
// (aliases after " as ") from an import target.
func cleanImportTarget(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, " as "); i > 0 {
		s = s[:i]
	}
	s = strings.Trim(s, "\"'`;")
	return strings.TrimSpace(s)
}

// dedupInfo removes duplicate query matches and sorts everything stably.
func dedupInfo(info *FileInfo) {
	seenDefs := make(map[Definition]bool)
	defs := info.Definitions[:0]
	for _, d := range info.Definitions {
		key := d
		key.Visibility = ""
		if !seenDefs[key] {
			seenDefs[key] = true
			defs = append(defs, d)
		}
	}
	info.Definitions = defs
	sort.SliceStable(info.Definitions, func(i, j int) bool {
		a, b := info.Definitions[i], info.Definitions[j]
		if a.StartLine != b.StartLine {
			return a.StartLine < b.StartLine
		}
		return a.Name < b.Name
	})

	seenImports := make(map[Import]bool)
	imports := info.Imports[:0]
	for _, imp := range info.Imports {
		if !seenImports[imp] {
			seenImports[imp] = true
			imports = append(imports, imp)
		}
	}
	info.Imports = imports
	sort.SliceStable(info.Imports, func(i, j int) bool {
		a, b := info.Imports[i], info.Imports[j]
		if a.StartLine != b.StartLine {
			return a.StartLine < b.StartLine
		}
		return a.Target < b.Target
	})

	seenModules := make(map[Module]bool)
	modules := info.Modules[:0]
	for _, m := range info.Modules {
		if !seenModules[m] {
			seenModules[m] = true
			modules = append(modules, m)
		}
	}
	info.Modules = modules
	sort.SliceStable(info.Modules, func(i, j int) bool {
		return info.Modules[i].StartLine < info.Modules[j].StartLine
	})
}
