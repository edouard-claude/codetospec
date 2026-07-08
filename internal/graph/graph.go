// Package graph assembles the spec knowledge graph from facts and reduced
// domain outputs, entirely deterministically.
package graph

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"codetospec/internal/extract"
	"codetospec/internal/reducer"
)

// Node is one markdown file in the spec graph.
type Node struct {
	ID      string // "rule.billing.prorata-activation"
	Type    string // "domain" | "entity" | "endpoint" | "rule"
	Status  string // always "generated" in v0.1
	Sources []extract.Ref
	Edges   []Edge
	Title   string
	Body    string            // markdown body without frontmatter
	Extra   map[string]string // ears, acceptance (rules only)
}

// Edge links two nodes.
type Edge struct {
	Type string `json:"type"` // "belongs_to" | "touches" | "exposed_by" | "depends_on"
	To   string `json:"to"`
}

// StatusGenerated is the only node status in v0.1.
const StatusGenerated = "generated"

// Build assembles the node set: entities from table facts, endpoints from
// route facts, rules and domains from the reduce outputs, and domain
// depends_on edges computed from cross-domain import facts (never by the LLM).
func Build(facts []extract.Fact, reduced []reducer.Output, resolver extract.DomainResolver) []Node {
	var nodes []Node
	nodes = append(nodes, entityNodes(facts)...)
	nodes = append(nodes, endpointNodes(facts)...)

	ruleNodes, domainNodes := ruleAndDomainNodes(reduced)
	domainDeps := domainDependencies(facts, resolver, domainNodes)
	for i := range domainNodes {
		slug := strings.TrimPrefix(domainNodes[i].ID, "domain.")
		domainNodes[i].Edges = append(domainNodes[i].Edges, domainDeps[slug]...)
		sortEdges(domainNodes[i].Edges)
	}
	nodes = append(nodes, ruleNodes...)
	nodes = append(nodes, domainNodes...)

	sort.SliceStable(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	return nodes
}

// EntityID derives the node id for a table fact.
func EntityID(f extract.Fact) string {
	name := f.Attrs["name"]
	if name == "" {
		name = strings.TrimPrefix(f.ID, "table.")
	}
	return "entity." + extract.Slugify(name)
}

// EndpointID derives the node id for a route fact.
func EndpointID(f extract.Fact) string {
	verb := strings.ToLower(routeVerb(f))
	return "endpoint." + extract.Slugify(verb+" "+f.Attrs["path"])
}

func routeVerb(f extract.Fact) string {
	if v := f.Attrs["method"]; v != "" {
		return v
	}
	return f.Attrs["verb"]
}

func routeHandler(f extract.Fact) string {
	handler := f.Attrs["handler"]
	if handler == "" {
		handler = f.Attrs["controller"]
		if action := f.Attrs["action"]; action != "" && handler != "" {
			handler += "@" + action
		}
	}
	return handler
}

func entityNodes(facts []extract.Fact) []Node {
	var nodes []Node
	for _, f := range facts {
		if f.Kind != "table" {
			continue
		}
		name := f.Attrs["name"]
		if name == "" {
			name = strings.TrimPrefix(f.ID, "table.")
		}
		var body strings.Builder
		body.WriteString("**Table** : `" + name + "`\n\n**Colonnes** :\n")
		for _, col := range splitColumns(f.Attrs["columns"]) {
			body.WriteString("- `" + col + "`\n")
		}
		nodes = append(nodes, Node{
			ID:      EntityID(f),
			Type:    "entity",
			Status:  StatusGenerated,
			Sources: []extract.Ref{f.Source},
			Title:   name,
			Body:    strings.TrimRight(body.String(), "\n"),
		})
	}
	return nodes
}

func splitColumns(columns string) []string {
	var cols []string
	for c := range strings.SplitSeq(columns, ",") {
		if c = strings.TrimSpace(c); c != "" {
			cols = append(cols, c)
		}
	}
	return cols
}

func endpointNodes(facts []extract.Fact) []Node {
	var nodes []Node
	for _, f := range facts {
		if f.Kind != "route" {
			continue
		}
		verb := strings.ToUpper(routeVerb(f))
		path := f.Attrs["path"]
		body := fmt.Sprintf("**Méthode** : %s\n\n**Path** : `%s`", verb, path)
		if handler := routeHandler(f); handler != "" {
			body += fmt.Sprintf("\n\n**Handler** : `%s`", handler)
		}
		nodes = append(nodes, Node{
			ID:      EndpointID(f),
			Type:    "endpoint",
			Status:  StatusGenerated,
			Sources: []extract.Ref{f.Source},
			Title:   verb + " " + path,
			Body:    body,
		})
	}
	return nodes
}

func ruleAndDomainNodes(reduced []reducer.Output) (rules, domains []Node) {
	for _, out := range reduced {
		if out.Failed || len(out.Rules) == 0 {
			continue
		}
		domainRules := append([]reducer.Rule(nil), out.Rules...)
		sort.SliceStable(domainRules, func(i, j int) bool { return domainRules[i].Slug < domainRules[j].Slug })

		var ruleList strings.Builder
		for _, rule := range domainRules {
			nodeID := fmt.Sprintf("rule.%s.%s", out.Domain, rule.Slug)
			edges := []Edge{{Type: "belongs_to", To: "domain." + out.Domain}}
			for _, e := range dedupSorted(rule.Entities) {
				edges = append(edges, Edge{Type: "touches", To: e})
			}
			for _, e := range dedupSorted(rule.Endpoints) {
				edges = append(edges, Edge{Type: "exposed_by", To: e})
			}
			sortEdges(edges)

			sources := append([]extract.Ref(nil), rule.Citations...)
			sort.SliceStable(sources, func(i, j int) bool {
				if sources[i].Path != sources[j].Path {
					return sources[i].Path < sources[j].Path
				}
				return sources[i].Lines < sources[j].Lines
			})

			var body strings.Builder
			body.WriteString("**Exigence (EARS)** : " + rule.Requirement)
			if rule.Rationale != "" {
				body.WriteString("\n\n**Justification** : " + rule.Rationale)
			}
			if len(rule.AcceptanceCriteria) > 0 {
				body.WriteString("\n\n**Critères d'acceptation** :")
				for i, criterion := range rule.AcceptanceCriteria {
					fmt.Fprintf(&body, "\n%d. %s", i+1, criterion)
				}
			}

			rules = append(rules, Node{
				ID:      nodeID,
				Type:    "rule",
				Status:  StatusGenerated,
				Sources: sources,
				Edges:   edges,
				Title:   rule.Title,
				Body:    body.String(),
				Extra: map[string]string{
					"ears":       rule.EarsKind,
					"acceptance": strconv.Itoa(len(rule.AcceptanceCriteria)),
				},
			})
			fmt.Fprintf(&ruleList, "- [%s](../rules/%s.%s.md)\n", rule.Title, out.Domain, rule.Slug)
		}

		body := out.DomainSummary
		if ruleList.Len() > 0 {
			body += "\n\n## Règles\n\n" + strings.TrimRight(ruleList.String(), "\n")
		}
		domains = append(domains, Node{
			ID:     "domain." + out.Domain,
			Type:   "domain",
			Status: StatusGenerated,
			Title:  "Domaine " + out.Domain,
			Body:   strings.TrimSpace(body),
		})
	}
	return rules, domains
}

// domainDependencies computes depends_on edges between existing domains from
// import facts whose source and target resolve to different domains.
func domainDependencies(facts []extract.Fact, resolver extract.DomainResolver, domains []Node) map[string][]Edge {
	existing := make(map[string]bool, len(domains))
	for _, d := range domains {
		existing[strings.TrimPrefix(d.ID, "domain.")] = true
	}

	type module struct {
		segments []string
		domain   string
	}
	var modules []module
	for _, f := range facts {
		if f.Kind != "module" {
			continue
		}
		name := f.Attrs["name"]
		if name == "" {
			continue
		}
		modules = append(modules, module{
			segments: extract.SplitQualified(name),
			domain:   resolver.Resolve(name, f.Source.Path),
		})
	}

	deps := make(map[string]map[string]bool)
	for _, f := range facts {
		if f.Kind != "import" {
			continue
		}
		from := resolver.Resolve(f.Attrs["namespace"], f.Source.Path)
		targetSegments := extract.SplitQualified(f.Attrs["target"])
		to, bestLen := "", 0
		for _, m := range modules {
			if len(m.segments) > bestLen && segmentsPrefix(m.segments, targetSegments) {
				to, bestLen = m.domain, len(m.segments)
			}
		}
		if to == "" || to == from || !existing[from] || !existing[to] {
			continue
		}
		if deps[from] == nil {
			deps[from] = make(map[string]bool)
		}
		deps[from][to] = true
	}

	edges := make(map[string][]Edge, len(deps))
	for from, targets := range deps {
		var list []Edge
		for to := range targets {
			list = append(list, Edge{Type: "depends_on", To: "domain." + to})
		}
		sortEdges(list)
		edges[from] = list
	}
	return edges
}

// segmentsPrefix reports whether prefix is a segment-wise prefix of full.
func segmentsPrefix(prefix, full []string) bool {
	if len(prefix) > len(full) {
		return false
	}
	for i, s := range prefix {
		if full[i] != s {
			return false
		}
	}
	return true
}

func sortEdges(edges []Edge) {
	sort.SliceStable(edges, func(i, j int) bool {
		if edges[i].Type != edges[j].Type {
			return edgeTypeRank(edges[i].Type) < edgeTypeRank(edges[j].Type)
		}
		return edges[i].To < edges[j].To
	})
}

func edgeTypeRank(t string) int {
	switch t {
	case "belongs_to":
		return 0
	case "touches":
		return 1
	case "exposed_by":
		return 2
	default: // depends_on
		return 3
	}
}

func dedupSorted(values []string) []string {
	seen := make(map[string]bool, len(values))
	var out []string
	for _, v := range values {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

// Coverage summarizes how much of the graph the rules reference.
type Coverage struct {
	EndpointsTotal      int
	EndpointsReferenced int
	EntitiesTotal       int
	EntitiesTouched     int
}

// ComputeCoverage counts endpoints and entities referenced by at least one
// rule edge.
func ComputeCoverage(nodes []Node) Coverage {
	var cov Coverage
	referenced := make(map[string]bool)
	for _, n := range nodes {
		if n.Type != "rule" {
			continue
		}
		for _, e := range n.Edges {
			if e.Type == "touches" || e.Type == "exposed_by" {
				referenced[e.To] = true
			}
		}
	}
	for _, n := range nodes {
		switch n.Type {
		case "endpoint":
			cov.EndpointsTotal++
			if referenced[n.ID] {
				cov.EndpointsReferenced++
			}
		case "entity":
			cov.EntitiesTotal++
			if referenced[n.ID] {
				cov.EntitiesTouched++
			}
		}
	}
	return cov
}
