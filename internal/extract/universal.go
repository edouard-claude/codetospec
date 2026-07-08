package extract

import (
	"fmt"

	"codetospec/internal/sitter"
)

// UniversalFacts converts tree-sitter file information into facts:
// one "symbol" fact per definition, one "module" fact per namespace unit
// and one "import" fact per dependency statement.
func UniversalFacts(path string, info *sitter.FileInfo) []Fact {
	if info == nil {
		return nil
	}
	facts := make([]Fact, 0, len(info.Definitions)+len(info.Modules)+len(info.Imports))

	for _, m := range info.Modules {
		facts = append(facts, Fact{
			Kind: "module",
			ID:   "module." + m.Name,
			Attrs: map[string]string{
				"name":     m.Name,
				"language": info.Language,
			},
			Source:    Ref{Path: path, Lines: lineRange(m.StartLine, m.EndLine)},
			Origin:    OriginSitter,
			Certainty: CertaintyStatic,
		})
	}

	for _, d := range info.Definitions {
		attrs := map[string]string{
			"name":     d.Name,
			"kind":     d.Kind,
			"language": info.Language,
		}
		if ns := namespaceFor(info.Modules, d.StartLine); ns != "" {
			attrs["namespace"] = ns
		}
		if container := containerFor(info.Definitions, d); container != "" {
			attrs["container"] = container
		}
		if d.Visibility != "" {
			attrs["visibility"] = d.Visibility
		}
		facts = append(facts, Fact{
			Kind:      "symbol",
			ID:        fmt.Sprintf("symbol.%s#%s:%d", path, d.Name, d.StartLine),
			Attrs:     attrs,
			Source:    Ref{Path: path, Lines: lineRange(d.StartLine, d.EndLine)},
			Origin:    OriginSitter,
			Certainty: CertaintyStatic,
		})
	}

	for _, imp := range info.Imports {
		attrs := map[string]string{"target": imp.Target}
		if ns := namespaceFor(info.Modules, imp.StartLine); ns != "" {
			attrs["namespace"] = ns
		}
		facts = append(facts, Fact{
			Kind:      "import",
			ID:        fmt.Sprintf("import.%s#%s", path, imp.Target),
			Attrs:     attrs,
			Source:    Ref{Path: path, Lines: lineRange(imp.StartLine, imp.EndLine)},
			Origin:    OriginSitter,
			Certainty: CertaintyStatic,
		})
	}
	return facts
}

func lineRange(start, end int) string {
	return fmt.Sprintf("%d-%d", start, end)
}

// namespaceFor returns the name of the last module declared at or before line.
func namespaceFor(modules []sitter.Module, line int) string {
	name := ""
	for _, m := range modules {
		if m.StartLine <= line {
			name = m.Name
		}
	}
	if name == "" && len(modules) > 0 {
		name = modules[0].Name
	}
	return name
}

// containerFor returns the name of the smallest definition strictly
// containing d (its class/struct for a method).
func containerFor(defs []sitter.Definition, d sitter.Definition) string {
	best := sitter.Definition{}
	found := false
	for _, other := range defs {
		if other == d {
			continue
		}
		strictlyContains := other.StartLine <= d.StartLine && d.EndLine <= other.EndLine &&
			(other.StartLine != d.StartLine || other.EndLine != d.EndLine)
		if !strictlyContains {
			continue
		}
		if !found || (other.EndLine-other.StartLine) < (best.EndLine-best.StartLine) {
			best, found = other, true
		}
	}
	if !found {
		return ""
	}
	return best.Name
}
