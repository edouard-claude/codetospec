// Command extract is the native Go facts extractor for codetospec. It speaks
// the codetospec/facts/v1 protocol: it prints a facts JSON envelope on stdout
// and free logs on stderr, so the codetospec binary consumes it without
// knowing anything about Go.
//
// It emits what tree-sitter cannot: `route` facts (gin/echo/chi/net-http,
// with group prefixes and handlers resolved via go/packages type info) and
// `table` facts (SQL CREATE TABLE from schema files, e.g. sqlc).
//
// Usage: extract --root <src> [--sql-glob "sql/schema/*.sql"]
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/tools/go/packages"
)

var excludedDirs = map[string]bool{
	"vendor": true, "node_modules": true, "testdata": true,
	"dist": true, "build": true, ".git": true, "tmp": true,
}

func main() {
	root := flag.String("root", "", "module root to analyze (required)")
	sqlGlob := flag.String("sql-glob", "", "optional glob (relative to root) restricting SQL schema files")
	flag.Parse()

	if *root == "" {
		fmt.Fprintln(os.Stderr, "extract: --root is required")
		os.Exit(1)
	}
	absRoot, err := filepath.Abs(*root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "extract: %v\n", err)
		os.Exit(1)
	}

	var facts []Fact
	facts = append(facts, routeFacts(absRoot)...)
	facts = append(facts, tableFacts(absRoot, *sqlGlob)...)

	sort.Slice(facts, func(i, j int) bool { return facts[i].ID < facts[j].ID })
	if err := json.NewEncoder(os.Stdout).Encode(Envelope{Schema: schemaID, Facts: facts}); err != nil {
		fmt.Fprintf(os.Stderr, "extract: encode: %v\n", err)
		os.Exit(1)
	}
}

// routeFacts loads the module with type information and converts detected
// routes to facts. Load errors are tolerated: partial results still ship.
func routeFacts(root string) []Fact {
	fset := token.NewFileSet()
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax |
			packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports | packages.NeedDeps,
		Dir:  root,
		Fset: fset,
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		fmt.Fprintf(os.Stderr, "extract: load packages: %v (routes skipped)\n", err)
		return nil
	}
	var loadErrors int
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		loadErrors += len(p.Errors)
	})
	if loadErrors > 0 {
		fmt.Fprintf(os.Stderr, "extract: %d package load error(s), continuing with partial type info\n", loadErrors)
	}

	var facts []Fact
	for _, r := range ExtractRoutes(pkgs, fset, root) {
		attrs := map[string]string{"path": r.Path}
		if r.Method != "" {
			attrs["method"] = r.Method
		}
		if r.Handler != "" && r.Handler != "closure" {
			attrs["handler"] = r.Handler
		}
		method := r.Method
		if method == "" {
			method = "any"
		}
		facts = append(facts, Fact{
			Kind:      "route",
			ID:        "route." + strings.ToLower(method) + "." + r.Path,
			Attrs:     attrs,
			Source:    r.Ref,
			Origin:    origin,
			Certainty: certain,
		})
	}
	fmt.Fprintf(os.Stderr, "extract: %d route(s)\n", len(facts))
	return facts
}

// tableFacts scans SQL files for CREATE TABLE statements.
func tableFacts(root, sqlGlob string) []Fact {
	var facts []Fact
	seen := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if excludedDirs[d.Name()] || strings.HasPrefix(d.Name(), ".") && d.Name() != "." {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(path), ".sql") {
			return nil
		}
		rel, relErr := relTo(root, path)
		if relErr != nil {
			return nil
		}
		if sqlGlob != "" {
			if ok, _ := filepath.Match(sqlGlob, rel); !ok {
				return nil
			}
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		for _, t := range ParseTables(string(data)) {
			if seen[t.Name] {
				continue
			}
			seen[t.Name] = true
			facts = append(facts, Fact{
				Kind:      "table",
				ID:        "table." + t.Name,
				Attrs:     map[string]string{"name": t.Name, "columns": strings.Join(t.Columns, ", ")},
				Source:    Ref{Path: rel, Lines: itoa(t.Line) + "-" + itoa(t.Line)},
				Origin:    origin,
				Certainty: certain,
			})
		}
		return nil
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "extract: scan sql: %v\n", err)
	}
	fmt.Fprintf(os.Stderr, "extract: %d table(s)\n", len(facts))
	return facts
}

func relTo(root, path string) (string, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(rel), nil
}

func itoa(n int) string { return strconv.Itoa(n) }
