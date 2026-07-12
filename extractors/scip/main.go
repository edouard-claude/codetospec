// Command scip2facts converts a SCIP code-intelligence index into codetospec
// facts (protocol facts v1). It emits `symbol` facts with the EXACT
// definition ranges an indexer resolved — far more precise than tree-sitter,
// which is the point: precise symbol locations let the map/verify phases stop
// citing empty lines or the wrong span.
//
// SCIP indexes are produced by language indexers (scip-go, scip-typescript,
// scip-clang, …). This tool reads their output; it never runs them.
//
// Usage: scip2facts --index index.scip [--root <src>]
//
// The tool speaks the extractor protocol, so codetospec consumes it via
// --facts or an extractor entry, with zero changes to the main binary.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"
)

func main() {
	indexPath := flag.String("index", "", "path to the SCIP index (index.scip) (required)")
	root := flag.String("root", "", "optional source root prefix to strip from document paths")
	flag.Parse()

	if *indexPath == "" {
		fmt.Fprintln(os.Stderr, "scip2facts: --index is required")
		os.Exit(1)
	}
	data, err := os.ReadFile(*indexPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "scip2facts: read index: %v\n", err)
		os.Exit(1)
	}
	var index scip.Index
	if err := proto.Unmarshal(data, &index); err != nil {
		fmt.Fprintf(os.Stderr, "scip2facts: parse SCIP index: %v\n", err)
		os.Exit(1)
	}

	facts := convert(&index, *root)
	sort.Slice(facts, func(i, j int) bool { return facts[i].ID < facts[j].ID })
	if err := json.NewEncoder(os.Stdout).Encode(Envelope{Schema: schemaID, Facts: facts}); err != nil {
		fmt.Fprintf(os.Stderr, "scip2facts: encode: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "scip2facts: %d symbol fact(s)\n", len(facts))
}

// convert turns definition occurrences into precise symbol facts.
func convert(index *scip.Index, root string) []Fact {
	var facts []Fact
	for _, doc := range index.GetDocuments() {
		path := normalizePath(doc.GetRelativePath(), root)
		if !isLocalPath(path) {
			continue // stdlib, dependencies, generated build artifacts
		}
		language := strings.ToLower(doc.GetLanguage())

		info := make(map[string]*scip.SymbolInformation, len(doc.GetSymbols()))
		for _, s := range doc.GetSymbols() {
			info[s.GetSymbol()] = s
		}

		for _, occ := range doc.GetOccurrences() {
			if occ.GetSymbolRoles()&int32(scip.SymbolRole_Definition) == 0 {
				continue
			}
			start, end, ok := definitionLines(occ)
			if !ok {
				continue
			}
			f := symbolFact(occ.GetSymbol(), info[occ.GetSymbol()], path, language, start, end)
			if f != nil {
				facts = append(facts, *f)
			}
		}
	}
	return dedup(facts)
}

// definitionLines returns the 1-based inclusive line span of a definition,
// preferring the enclosing (body) range over the identifier range. It reads
// the raw []int32 range fields, which is what indexers actually emit.
func definitionLines(occ *scip.Occurrence) (int, int, bool) {
	raw := occ.GetEnclosingRange() //nolint:staticcheck // indexers emit the raw range field
	if len(raw) == 0 {
		raw = occ.GetRange() //nolint:staticcheck // indexers emit the raw range field
	}
	r, err := scip.NewRange(raw)
	if err != nil {
		return 0, 0, false
	}
	return int(r.Start.Line) + 1, int(r.End.Line) + 1, true
}

// symbolFact builds a symbol fact from a definition, resolving the name,
// kind, namespace and signature from SCIP metadata when available.
func symbolFact(symbol string, si *scip.SymbolInformation, path, language string, start, end int) *Fact {
	name := ""
	kind := ""
	namespace := ""
	signature := ""
	if si != nil {
		name = si.GetDisplayName()
		kind = symbolKind(si.GetKind())
		if sig := si.GetSignatureDocumentation(); sig != nil {
			signature = strings.TrimSpace(sig.GetText())
		}
	}
	if parsed, err := scip.ParseSymbol(symbol); err == nil {
		if name == "" && len(parsed.Descriptors) > 0 {
			name = parsed.Descriptors[len(parsed.Descriptors)-1].Name
		}
		namespace = qualifier(parsed)
		if language == "" && parsed.Package != nil {
			language = ""
		}
	}
	if name == "" {
		return nil // an anonymous or unresolvable symbol is not useful
	}

	attrs := map[string]string{"name": name, "precise": "true"}
	if kind != "" {
		attrs["kind"] = kind
	}
	if language != "" {
		attrs["language"] = language
	}
	if namespace != "" {
		attrs["namespace"] = namespace
	}
	if signature != "" {
		attrs["signature"] = truncate(signature, 400)
	}
	return &Fact{
		Kind:      "symbol",
		ID:        fmt.Sprintf("symbol.%s#%s:%d", path, name, start),
		Attrs:     attrs,
		Source:    Ref{Path: path, Lines: fmt.Sprintf("%d-%d", start, end)},
		Origin:    origin,
		Certainty: certain,
	}
}

// qualifier joins the enclosing descriptors (namespace/type path) of a
// symbol, excluding its own final descriptor.
func qualifier(sym *scip.Symbol) string {
	if len(sym.Descriptors) < 2 {
		return ""
	}
	parts := make([]string, 0, len(sym.Descriptors)-1)
	for _, d := range sym.Descriptors[:len(sym.Descriptors)-1] {
		if d.Name != "" {
			parts = append(parts, d.Name)
		}
	}
	return strings.Join(parts, ".")
}

func symbolKind(k scip.SymbolInformation_Kind) string {
	switch k {
	case scip.SymbolInformation_Class, scip.SymbolInformation_Struct:
		return "class"
	case scip.SymbolInformation_Interface:
		return "interface"
	case scip.SymbolInformation_Method:
		return "method"
	case scip.SymbolInformation_Function:
		return "function"
	case scip.SymbolInformation_Enum:
		return "enum"
	case scip.SymbolInformation_Trait:
		return "trait"
	default:
		return ""
	}
}

func normalizePath(path, root string) string {
	path = strings.TrimPrefix(path, "./")
	if root != "" {
		path = strings.TrimPrefix(path, strings.TrimSuffix(root, "/")+"/")
	}
	return path
}

// isLocalPath keeps only paths that live inside the analyzed repository —
// not stdlib, dependencies or generated build artifacts an indexer may pull
// in from outside the tree.
func isLocalPath(path string) bool {
	if path == "" || strings.HasPrefix(path, "..") || strings.HasPrefix(path, "/") {
		return false
	}
	return !strings.Contains(path, "go-build") && !strings.Contains(path, "/pkg/mod/")
}

func dedup(facts []Fact) []Fact {
	seen := make(map[string]bool, len(facts))
	out := facts[:0]
	for _, f := range facts {
		if !seen[f.ID] {
			seen[f.ID] = true
			out = append(out, f)
		}
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
