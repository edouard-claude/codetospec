// Package verify checks graph integrity against the source tree: unique ids,
// resolvable edges and citations, cited rules, and frontmatter round-trip.
package verify

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"codetospec/internal/extract"
	"codetospec/internal/graph"
	"codetospec/internal/render"
	"codetospec/internal/sitter"
)

// Violation is one failed integrity check.
type Violation struct {
	NodeID string
	Check  string
	Detail string
}

func (v Violation) String() string {
	return fmt.Sprintf("%s: %s: %s", v.NodeID, v.Check, v.Detail)
}

// Run performs all integrity checks on the graph. An empty result means the
// graph may be rendered.
func Run(nodes []graph.Node, srcRoot string) []Violation {
	var violations []Violation

	ids := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		if ids[n.ID] {
			violations = append(violations, Violation{n.ID, "duplicate-id", "node id appears more than once"})
		}
		ids[n.ID] = true
	}

	lineCounts := make(map[string]int)
	for _, n := range nodes {
		for _, e := range n.Edges {
			if !ids[e.To] {
				violations = append(violations, Violation{n.ID, "dangling-edge",
					fmt.Sprintf("edge %s points to missing node %s", e.Type, e.To)})
			}
		}
		if n.Type == "rule" && len(n.Sources) == 0 {
			violations = append(violations, Violation{n.ID, "rule-without-citation", "rule node has no source citation"})
		}
		for _, s := range n.Sources {
			if detail := checkCitation(srcRoot, s.Path, s.Lines, lineCounts); detail != "" {
				violations = append(violations, Violation{n.ID, "unresolvable-citation", detail})
			}
		}
		if detail := checkRoundTrip(n); detail != "" {
			violations = append(violations, Violation{n.ID, "frontmatter-round-trip", detail})
		}
	}
	return violations
}

// checkCitation verifies that path exists under srcRoot and that the line
// range fits inside the real file.
func checkCitation(srcRoot, path, lines string, lineCounts map[string]int) string {
	if !filepath.IsLocal(filepath.FromSlash(path)) {
		return fmt.Sprintf("path %q escapes the source root", path)
	}
	_, end, err := extract.ParseLines(lines)
	if err != nil {
		return err.Error()
	}
	count, ok := lineCounts[path]
	if !ok {
		data, readErr := os.ReadFile(filepath.Join(srcRoot, filepath.FromSlash(path)))
		if readErr != nil {
			lineCounts[path] = -1
			return fmt.Sprintf("path %q not found under source root", path)
		}
		count = sitter.CountLines(data)
		lineCounts[path] = count
	}
	if count < 0 {
		return fmt.Sprintf("path %q not found under source root", path)
	}
	if end > count {
		return fmt.Sprintf("lines %s exceed file %s (%d lines)", lines, path, count)
	}
	return ""
}

// checkRoundTrip re-renders the node and re-parses its frontmatter,
// verifying that nothing is lost or altered.
func checkRoundTrip(n graph.Node) string {
	parsed, err := render.ParseNode(render.NodeMarkdown(n))
	if err != nil {
		return fmt.Sprintf("regenerated frontmatter is not parsable: %v", err)
	}
	if parsed.ID != n.ID || parsed.Type != n.Type || parsed.Status != n.Status {
		return "id/type/status did not survive the round-trip"
	}
	if !reflect.DeepEqual(parsed.Sources, n.Sources) {
		return "sources did not survive the round-trip"
	}
	if !reflect.DeepEqual(parsed.Edges, n.Edges) {
		return "edges did not survive the round-trip"
	}
	if parsed.Extra["ears"] != n.Extra["ears"] || parsed.Extra["acceptance"] != n.Extra["acceptance"] {
		return "ears/acceptance did not survive the round-trip"
	}
	return ""
}
