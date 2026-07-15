// Package extract defines the Fact model shared by all extraction layers,
// the merge/dedup logic and the domain resolution rules.
package extract

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Fact is a mechanically provable statement about the codebase.
type Fact struct {
	Kind      string            `json:"kind"` // "symbol", "route", "table", "module", "import"
	ID        string            `json:"id"`   // stable identifier
	Attrs     map[string]string `json:"attrs"`
	Source    Ref               `json:"source"`
	Origin    string            `json:"origin"`    // "sitter" | extractor name | facts file
	Certainty string            `json:"certainty"` // "proved" | "static"
}

// Ref pins a fact to a file location.
type Ref struct {
	Path  string `json:"path"`  // relative to --src, slash-separated
	Lines string `json:"lines"` // "12-87", 1-based inclusive
}

// Certainty and origin constants used across the pipeline.
const (
	CertaintyProved = "proved"
	CertaintyStatic = "static"
	OriginSitter    = "sitter"
)

// Merge concatenates fact sets and deduplicates by ID with priority
// proved > external extractor > sitter. On equal priority the first
// occurrence wins. The result is sorted by ID for determinism.
func Merge(sets ...[]Fact) []Fact {
	byID := make(map[string]Fact)
	var order []string
	for _, set := range sets {
		for _, f := range set {
			existing, ok := byID[f.ID]
			if !ok {
				byID[f.ID] = f
				order = append(order, f.ID)
				continue
			}
			if factRank(f) > factRank(existing) {
				byID[f.ID] = f
			}
		}
	}
	sort.Strings(order)
	merged := make([]Fact, 0, len(order))
	for _, id := range order {
		merged = append(merged, byID[id])
	}
	return merged
}

// factRank orders fact precedence: proved beats external extractors,
// which beat the universal tree-sitter layer.
func factRank(f Fact) int {
	switch {
	case f.Certainty == CertaintyProved:
		return 3
	case f.Origin != OriginSitter:
		return 2
	default:
		return 1
	}
}

// DomainResolver derives domain slugs according to the configured strategy.
type DomainResolver struct {
	Strategy string // "auto" | "namespace" | "directory"
	// Depth is how many namespace segments (after the root prefix) form the
	// domain. 0 or 1 keeps a single segment ("core"); 2 gives
	// "core-controller", 3 "core-controller-sdk" — useful when a whole repo
	// lives under one root namespace and would otherwise collapse to one
	// mega-domain.
	Depth int
}

// Resolve returns the domain slug for a namespace/path pair.
func (r DomainResolver) Resolve(namespace, path string) string {
	strategy := r.Strategy
	if strategy == "" {
		strategy = "auto"
	}
	if strategy != "directory" && namespace != "" {
		if d := domainFromNamespace(namespace, r.Depth); d != "" {
			return d
		}
	}
	return domainFromPath(path)
}

// DomainOf derives the domain slug for a fact using the auto strategy:
// namespace attribute when available, first directory under --src otherwise.
func DomainOf(f Fact, path string) string {
	return DomainResolver{Strategy: "auto"}.Resolve(f.Attrs["namespace"], path)
}

// domainFromNamespace joins the `depth` segments after the common root
// prefix (first segment) of a qualified namespace, lowercased and slugified.
func domainFromNamespace(namespace string, depth int) string {
	segments := SplitQualified(namespace)
	if len(segments) == 0 {
		return ""
	}
	if len(segments) == 1 {
		return Slugify(segments[0])
	}
	if depth < 1 {
		depth = 1
	}
	start := 1
	end := min(start+depth, len(segments))
	return Slugify(strings.Join(segments[start:end], "-"))
}

// domainFromPath picks the first directory under the source root.
func domainFromPath(path string) string {
	path = strings.TrimPrefix(strings.TrimPrefix(path, "./"), "/")
	if i := strings.IndexByte(path, '/'); i > 0 {
		return Slugify(path[:i])
	}
	return "root"
}

var qualifiedSeparators = regexp.MustCompile(`\\+|::|/`)

// SplitQualified splits a qualified name (PHP namespace, Go import path,
// Rust module path) into its segments.
func SplitQualified(name string) []string {
	parts := qualifiedSeparators.Split(name, -1)
	segments := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			segments = append(segments, p)
		}
	}
	return segments
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// Slugify lowercases s and collapses non-alphanumeric runs into dashes.
func Slugify(s string) string {
	s = nonSlug.ReplaceAllString(strings.ToLower(s), "-")
	return strings.Trim(s, "-")
}

var linesFormat = regexp.MustCompile(`^(\d+)-(\d+)$`)

// ParseLines parses a "A-B" 1-based inclusive line range.
func ParseLines(s string) (int, int, error) {
	m := linesFormat.FindStringSubmatch(s)
	if m == nil {
		return 0, 0, fmt.Errorf("invalid line range %q, want \"A-B\"", s)
	}
	a, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid line range %q: %w", s, err)
	}
	b, err := strconv.Atoi(m[2])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid line range %q: %w", s, err)
	}
	if a < 1 || b < a {
		return 0, 0, fmt.Errorf("invalid line range %q: bounds out of order", s)
	}
	return a, b, nil
}
