// Package render writes the graph to disk: markdown nodes with YAML
// frontmatter, graph.json, README.md and llms.txt. Output is byte-for-byte
// deterministic for a given graph.
package render

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"codetospec/internal/extract"
	"codetospec/internal/graph"
)

// Meta is run-level information rendered into README.md.
type Meta struct {
	Coverage         graph.Coverage
	ChunksFailed     int
	ChunksTotal      int
	DomainsFailed    int
	DomainsTotal     int
	FilesNoGrammar   []string
	ExtractorsFailed []string
	// Adversarial cross-check tally; only rendered when Crosschecked is true.
	Crosschecked          bool
	CrosscheckSupported   int
	CrosscheckPartial     int
	CrosscheckUnsupported int
	CrosscheckRepaired    int
	CrosscheckFailed      int
}

// Write renders the whole graph into outDir: nodes/, graph.json, README.md
// and llms.txt. nodes/ is emptied then rewritten; .codetospec/ is never
// touched.
func Write(outDir string, nodes []graph.Node, meta Meta) error {
	nodesDir := filepath.Join(outDir, "nodes")
	if err := os.RemoveAll(nodesDir); err != nil {
		return fmt.Errorf("clear nodes dir: %w", err)
	}
	for _, sub := range []string{"domains", "entities", "endpoints", "rules"} {
		if err := os.MkdirAll(filepath.Join(nodesDir, sub), 0o755); err != nil {
			return fmt.Errorf("create nodes dir: %w", err)
		}
	}
	for _, n := range nodes {
		path := filepath.Join(outDir, filepath.FromSlash(NodeFile(n)))
		if err := os.WriteFile(path, []byte(NodeMarkdown(n)), 0o644); err != nil {
			return fmt.Errorf("write node %s: %w", n.ID, err)
		}
	}
	if err := writeGraphJSON(outDir, nodes); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outDir, "README.md"), []byte(readme(nodes, meta)), 0o644); err != nil {
		return fmt.Errorf("write README.md: %w", err)
	}
	if err := os.WriteFile(filepath.Join(outDir, "llms.txt"), []byte(llmsTxt(nodes)), 0o644); err != nil {
		return fmt.Errorf("write llms.txt: %w", err)
	}
	return nil
}

// NodeFile returns the path of a node file relative to the output dir.
func NodeFile(n graph.Node) string {
	switch n.Type {
	case "domain":
		return "nodes/domains/" + strings.TrimPrefix(n.ID, "domain.") + ".md"
	case "entity":
		return "nodes/entities/" + strings.TrimPrefix(n.ID, "entity.") + ".md"
	case "endpoint":
		return "nodes/endpoints/" + strings.TrimPrefix(n.ID, "endpoint.") + ".md"
	default:
		return "nodes/rules/" + strings.TrimPrefix(n.ID, "rule.") + ".md"
	}
}

// Frontmatter renders the exact YAML frontmatter of a node.
func Frontmatter(n graph.Node) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "id: %s\n", n.ID)
	fmt.Fprintf(&b, "type: %s\n", n.Type)
	fmt.Fprintf(&b, "status: %s\n", n.Status)
	if len(n.Sources) == 0 {
		b.WriteString("sources: []\n")
	} else {
		b.WriteString("sources:\n")
		for _, s := range n.Sources {
			fmt.Fprintf(&b, "  - path: %s\n    lines: %q\n", s.Path, s.Lines)
		}
	}
	if len(n.Edges) == 0 {
		b.WriteString("edges: []\n")
	} else {
		b.WriteString("edges:\n")
		for _, e := range n.Edges {
			fmt.Fprintf(&b, "  - {type: %s, to: %s}\n", e.Type, e.To)
		}
	}
	if v := n.Extra["ears"]; v != "" {
		fmt.Fprintf(&b, "ears: %s\n", v)
	}
	if v := n.Extra["acceptance"]; v != "" {
		fmt.Fprintf(&b, "acceptance: %s\n", v)
	}
	if v := n.Extra["nature"]; v != "" {
		fmt.Fprintf(&b, "nature: %s\n", v)
	}
	if v := n.Extra["origin"]; v != "" {
		fmt.Fprintf(&b, "origin: %s\n", v)
	}
	if v := n.Extra["confidence"]; v != "" {
		fmt.Fprintf(&b, "confidence: %s\n", v)
	}
	if v := n.Extra["crosscheck"]; v != "" {
		fmt.Fprintf(&b, "crosscheck: %s\n", v)
	}
	b.WriteString("---\n")
	return b.String()
}

// NodeMarkdown renders one full node file: frontmatter, title, body,
// sources line and inline links for every edge.
func NodeMarkdown(n graph.Node) string {
	var b strings.Builder
	b.WriteString(Frontmatter(n))
	b.WriteString("\n# " + n.Title + "\n")
	if n.Body != "" {
		b.WriteString("\n" + n.Body + "\n")
	}
	if len(n.Sources) > 0 {
		parts := make([]string, len(n.Sources))
		for i, s := range n.Sources {
			parts[i] = fmt.Sprintf("`%s:%s`", s.Path, s.Lines)
		}
		b.WriteString("\n**Sources** : " + strings.Join(parts, " · ") + "\n")
	}
	if len(n.Edges) > 0 {
		links := make([]string, len(n.Edges))
		for i, e := range n.Edges {
			links[i] = edgeLink(e)
		}
		b.WriteString("\nLiens : " + strings.Join(links, " · ") + "\n")
	}
	return b.String()
}

// edgeLink renders one inline markdown link with a correct relative path
// (every node lives one level under nodes/).
func edgeLink(e graph.Edge) string {
	switch {
	case strings.HasPrefix(e.To, "domain."):
		slug := strings.TrimPrefix(e.To, "domain.")
		text := "Domaine " + slug
		if e.Type == "depends_on" {
			text = e.To
		}
		return fmt.Sprintf("[%s](../domains/%s.md)", text, slug)
	case strings.HasPrefix(e.To, "entity."):
		return fmt.Sprintf("[%s](../entities/%s.md)", e.To, strings.TrimPrefix(e.To, "entity."))
	case strings.HasPrefix(e.To, "endpoint."):
		return fmt.Sprintf("[%s](../endpoints/%s.md)", e.To, strings.TrimPrefix(e.To, "endpoint."))
	case strings.HasPrefix(e.To, "rule."):
		return fmt.Sprintf("[%s](../rules/%s.md)", e.To, strings.TrimPrefix(e.To, "rule."))
	default:
		return e.To
	}
}

// graphJSONNode is one entry of graph.json.
type graphJSONNode struct {
	Type  string       `json:"type"`
	File  string       `json:"file"`
	Edges []graph.Edge `json:"edges"`
}

func writeGraphJSON(outDir string, nodes []graph.Node) error {
	entries := make(map[string]graphJSONNode, len(nodes))
	for _, n := range nodes {
		edges := n.Edges
		if edges == nil {
			edges = []graph.Edge{}
		}
		entries[n.ID] = graphJSONNode{Type: n.Type, File: NodeFile(n), Edges: edges}
	}
	data, err := json.MarshalIndent(map[string]any{"nodes": entries}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode graph.json: %w", err)
	}
	if err := os.WriteFile(filepath.Join(outDir, "graph.json"), append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write graph.json: %w", err)
	}
	return nil
}

func countByType(nodes []graph.Node) map[string]int {
	counts := make(map[string]int)
	for _, n := range nodes {
		counts[n.Type]++
	}
	return counts
}

// countByExtra tallies rule nodes by the value of one Extra key.
func countByExtra(nodes []graph.Node, key string) map[string]int {
	counts := make(map[string]int)
	for _, n := range nodes {
		if n.Type == "rule" && n.Extra[key] != "" {
			counts[n.Extra[key]]++
		}
	}
	return counts
}

func percent(part, total int) string {
	if total == 0 {
		return "n/a"
	}
	return strconv.Itoa(part*100/total) + "%"
}

func readme(nodes []graph.Node, meta Meta) string {
	counts := countByType(nodes)
	var b strings.Builder
	b.WriteString("# Spécification générée\n\n")
	b.WriteString("Graphe de connaissances des règles métier, extrait du code source par codetospec.\n\n")
	fmt.Fprintf(&b, "- Domaines : %d\n", counts["domain"])
	fmt.Fprintf(&b, "- Entités : %d\n", counts["entity"])
	fmt.Fprintf(&b, "- Endpoints : %d\n", counts["endpoint"])
	fmt.Fprintf(&b, "- Règles : %d\n", counts["rule"])

	byNature := countByExtra(nodes, "nature")
	if total := byNature["business"] + byNature["presentation"] + byNature["technical"]; total > 0 {
		fmt.Fprintf(&b, "  - métier : %d · présentation : %d · technique : %d\n",
			byNature["business"], byNature["presentation"], byNature["technical"])
	}
	byOrigin := countByExtra(nodes, "origin")
	if byOrigin["explicit"]+byOrigin["implicit"] > 0 {
		fmt.Fprintf(&b, "  - explicites : %d · implicites : %d\n", byOrigin["explicit"], byOrigin["implicit"])
	}

	b.WriteString("\n## Couverture\n\n")
	fmt.Fprintf(&b, "- Endpoints référencés par au moins une règle : %d/%d (%s)\n",
		meta.Coverage.EndpointsReferenced, meta.Coverage.EndpointsTotal,
		percent(meta.Coverage.EndpointsReferenced, meta.Coverage.EndpointsTotal))
	fmt.Fprintf(&b, "- Entités touchées par au moins une règle : %d/%d (%s)\n",
		meta.Coverage.EntitiesTouched, meta.Coverage.EntitiesTotal,
		percent(meta.Coverage.EntitiesTouched, meta.Coverage.EntitiesTotal))
	fmt.Fprintf(&b, "- Chunks en échec : %d/%d\n", meta.ChunksFailed, meta.ChunksTotal)
	fmt.Fprintf(&b, "- Domaines en échec : %d/%d\n", meta.DomainsFailed, meta.DomainsTotal)
	if len(meta.FilesNoGrammar) > 0 {
		files := append([]string(nil), meta.FilesNoGrammar...)
		sort.Strings(files)
		fmt.Fprintf(&b, "- Fichiers sans grammaire (fallback lignes) : %s\n", strings.Join(files, ", "))
	}
	if len(meta.ExtractorsFailed) > 0 {
		names := append([]string(nil), meta.ExtractorsFailed...)
		sort.Strings(names)
		fmt.Fprintf(&b, "- Extracteurs en échec : %s\n", strings.Join(names, ", "))
	}
	if meta.Crosschecked {
		fmt.Fprintf(&b, "- Contre-vérification adversariale : %d supported, %d partial, %d unsupported",
			meta.CrosscheckSupported, meta.CrosscheckPartial, meta.CrosscheckUnsupported)
		if meta.CrosscheckRepaired > 0 {
			fmt.Fprintf(&b, ", %d réparés", meta.CrosscheckRepaired)
		}
		if meta.CrosscheckFailed > 0 {
			fmt.Fprintf(&b, ", %d en échec", meta.CrosscheckFailed)
		}
		b.WriteString("\n")
	}

	b.WriteString("\n## Domaines\n\n```mermaid\ngraph TD\n")
	for _, n := range nodes {
		if n.Type == "domain" {
			slug := strings.TrimPrefix(n.ID, "domain.")
			fmt.Fprintf(&b, "  %s[\"%s\"]\n", mermaidID(slug), slug)
		}
	}
	for _, n := range nodes {
		if n.Type != "domain" {
			continue
		}
		from := mermaidID(strings.TrimPrefix(n.ID, "domain."))
		for _, e := range n.Edges {
			if e.Type == "depends_on" {
				fmt.Fprintf(&b, "  %s --> %s\n", from, mermaidID(strings.TrimPrefix(e.To, "domain.")))
			}
		}
	}
	b.WriteString("```\n")
	return b.String()
}

// mermaidID makes a slug safe as a Mermaid node identifier.
func mermaidID(slug string) string {
	return strings.ReplaceAll(slug, "-", "_")
}

func llmsTxt(nodes []graph.Node) string {
	var b strings.Builder
	b.WriteString("# codetospec knowledge graph\n\n")
	b.WriteString("> Spécification des règles métier extraite automatiquement du code source.\n")
	b.WriteString("> Chaque nœud est un fichier markdown avec un frontmatter YAML portant des edges typés.\n\n")
	b.WriteString("Navigation : commencer par nodes/domains/, puis suivre les edges du frontmatter\n")
	b.WriteString("(belongs_to, touches, exposed_by, depends_on). graph.json contient le graphe complet\n")
	b.WriteString("au format machine ; les citations `path:lines` pointent dans le dépôt source.\n")
	for _, nodeType := range []string{"domain", "entity", "endpoint", "rule"} {
		var lines []string
		for _, n := range nodes {
			if n.Type == nodeType {
				lines = append(lines, fmt.Sprintf("- %s : %s", NodeFile(n), n.Title))
			}
		}
		if len(lines) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n## %s\n\n", nodeType)
		b.WriteString(strings.Join(lines, "\n") + "\n")
	}
	return b.String()
}

// frontmatterDoc mirrors the YAML frontmatter for parsing.
type frontmatterDoc struct {
	ID      string `yaml:"id"`
	Type    string `yaml:"type"`
	Status  string `yaml:"status"`
	Sources []struct {
		Path  string `yaml:"path"`
		Lines string `yaml:"lines"`
	} `yaml:"sources"`
	Edges []struct {
		Type string `yaml:"type"`
		To   string `yaml:"to"`
	} `yaml:"edges"`
	Ears       string   `yaml:"ears"`
	Acceptance *int     `yaml:"acceptance"`
	Nature     string   `yaml:"nature"`
	Origin     string   `yaml:"origin"`
	Confidence *float64 `yaml:"confidence"`
	Crosscheck string   `yaml:"crosscheck"`
}

// ParseNode parses one rendered node file back into a Node (frontmatter
// fields and title; the body is kept verbatim). Used by the round-trip
// check and the verify command.
func ParseNode(content string) (graph.Node, error) {
	var n graph.Node
	rest, ok := strings.CutPrefix(content, "---\n")
	if !ok {
		return n, fmt.Errorf("missing frontmatter opening")
	}
	front, body, ok := strings.Cut(rest, "\n---\n")
	if !ok {
		return n, fmt.Errorf("missing frontmatter closing")
	}
	var doc frontmatterDoc
	if err := yaml.Unmarshal([]byte(front), &doc); err != nil {
		return n, fmt.Errorf("parse frontmatter: %w", err)
	}
	n.ID = doc.ID
	n.Type = doc.Type
	n.Status = doc.Status
	for _, s := range doc.Sources {
		n.Sources = append(n.Sources, extract.Ref{Path: s.Path, Lines: s.Lines})
	}
	for _, e := range doc.Edges {
		n.Edges = append(n.Edges, graph.Edge{Type: e.Type, To: e.To})
	}
	extra := map[string]string{}
	if doc.Ears != "" {
		extra["ears"] = doc.Ears
	}
	if doc.Acceptance != nil {
		extra["acceptance"] = strconv.Itoa(*doc.Acceptance)
	}
	if doc.Nature != "" {
		extra["nature"] = doc.Nature
	}
	if doc.Origin != "" {
		extra["origin"] = doc.Origin
	}
	if doc.Confidence != nil {
		extra["confidence"] = strconv.FormatFloat(*doc.Confidence, 'f', 2, 64)
	}
	if doc.Crosscheck != "" {
		extra["crosscheck"] = doc.Crosscheck
	}
	if len(extra) > 0 {
		n.Extra = extra
	}
	for line := range strings.SplitSeq(body, "\n") {
		if title, ok := strings.CutPrefix(line, "# "); ok {
			n.Title = title
			break
		}
	}
	n.Body = body
	return n, nil
}

// LoadDir reads every node file under outDir/nodes for re-verification.
func LoadDir(outDir string) ([]graph.Node, error) {
	pattern := filepath.Join(outDir, "nodes", "*", "*.md")
	paths, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("glob %s: %w", pattern, err)
	}
	sort.Strings(paths)
	var nodes []graph.Node
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read node %s: %w", path, err)
		}
		n, err := ParseNode(string(data))
		if err != nil {
			return nil, fmt.Errorf("parse node %s: %w", path, err)
		}
		nodes = append(nodes, n)
	}
	return nodes, nil
}
