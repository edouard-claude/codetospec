package graph

import (
	"strings"
	"testing"

	"codetospec/internal/extract"
	"codetospec/internal/reducer"
)

func fixtureFacts() []extract.Fact {
	return []extract.Fact{
		{
			Kind: "table", ID: "table.invoices",
			Attrs:  map[string]string{"name": "invoices", "columns": "id:id, amount:decimal"},
			Source: extract.Ref{Path: "database/migrations/m.php", Lines: "11-16"},
		},
		{
			Kind: "route", ID: "route.post./api/activate",
			Attrs:  map[string]string{"method": "POST", "path": "/api/activate", "controller": `App\Http\Controllers\ActivationController`, "action": "store"},
			Source: extract.Ref{Path: "routes/web.php", Lines: "5-5"},
		},
		{
			Kind: "module", ID: "module.App\\Http\\Controllers",
			Attrs:  map[string]string{"name": `App\Http\Controllers`},
			Source: extract.Ref{Path: "app/Http/Controllers/ActivationController.php", Lines: "3-3"},
		},
		{
			Kind: "module", ID: "module.App\\Services\\Billing",
			Attrs:  map[string]string{"name": `App\Services\Billing`},
			Source: extract.Ref{Path: "app/Services/Billing/ProrataCalculator.php", Lines: "3-3"},
		},
		{
			Kind: "import", ID: "import.app/Http/Controllers/ActivationController.php#App\\Services\\Billing\\ProrataCalculator",
			Attrs:  map[string]string{"target": `App\Services\Billing\ProrataCalculator`, "namespace": `App\Http\Controllers`},
			Source: extract.Ref{Path: "app/Http/Controllers/ActivationController.php", Lines: "6-6"},
		},
	}
}

func fixtureReduced() []reducer.Output {
	rule := func(slug string) reducer.Rule {
		return reducer.Rule{
			Slug:               slug,
			Title:              "Titre " + slug,
			EarsKind:           "event",
			Requirement:        "QUAND x, le systeme doit y.",
			Rationale:          "Parce que.",
			Citations:          []extract.Ref{{Path: "app/Services/Billing/ProrataCalculator.php", Lines: "11-24"}},
			Entities:           []string{"entity.invoices"},
			Endpoints:          []string{"endpoint.post-api-activate"},
			AcceptanceCriteria: []string{"a", "b", "c"},
		}
	}
	return []reducer.Output{
		{Domain: "services", DomainSummary: "Calculs.", Rules: []reducer.Rule{rule("prorata-activation")}},
		{Domain: "http", DomainSummary: "Entrées HTTP.", Rules: []reducer.Rule{rule("activation-endpoint")}},
		{Domain: "failed", Failed: true},
	}
}

func TestBuildProducesAllNodeTypes(t *testing.T) {
	nodes := Build(fixtureFacts(), fixtureReduced(), extract.DomainResolver{})
	byID := make(map[string]Node, len(nodes))
	for _, n := range nodes {
		byID[n.ID] = n
	}

	for _, id := range []string{
		"entity.invoices",
		"endpoint.post-api-activate",
		"domain.services",
		"domain.http",
		"rule.services.prorata-activation",
		"rule.http.activation-endpoint",
	} {
		if _, ok := byID[id]; !ok {
			t.Errorf("node %s missing (have %d nodes)", id, len(nodes))
		}
	}
	if _, ok := byID["domain.failed"]; ok {
		t.Error("failed domain must not produce a node")
	}

	rule := byID["rule.services.prorata-activation"]
	if rule.Extra["ears"] != "event" || rule.Extra["acceptance"] != "3" {
		t.Errorf("rule extra = %v", rule.Extra)
	}
	wantEdges := []Edge{
		{Type: "belongs_to", To: "domain.services"},
		{Type: "touches", To: "entity.invoices"},
		{Type: "exposed_by", To: "endpoint.post-api-activate"},
	}
	if len(rule.Edges) != len(wantEdges) {
		t.Fatalf("rule edges = %+v", rule.Edges)
	}
	for i, want := range wantEdges {
		if rule.Edges[i] != want {
			t.Errorf("rule edge[%d] = %+v, want %+v", i, rule.Edges[i], want)
		}
	}

	endpoint := byID["endpoint.post-api-activate"]
	if !strings.Contains(endpoint.Body, "POST") || !strings.Contains(endpoint.Body, "/api/activate") ||
		!strings.Contains(endpoint.Body, "ActivationController@store") {
		t.Errorf("endpoint body = %q", endpoint.Body)
	}
}

// TestBuildDomainDependsOnGoStyleImports covers the suffix fallback: Go
// imports carry a path ("app/internal/billing") while module facts carry the
// bare package name ("billing").
func TestBuildDomainDependsOnGoStyleImports(t *testing.T) {
	facts := []extract.Fact{
		{
			Kind: "module", ID: "module.billing",
			Attrs:  map[string]string{"name": "billing"},
			Source: extract.Ref{Path: "internal/billing/calc.go", Lines: "1-1"},
		},
		{
			Kind: "module", ID: "module.invoice",
			Attrs:  map[string]string{"name": "invoice"},
			Source: extract.Ref{Path: "internal/invoice/invoice.go", Lines: "1-1"},
		},
		{
			Kind: "import", ID: "import.internal/invoice/invoice.go#app/internal/billing",
			Attrs:  map[string]string{"target": "app/internal/billing", "namespace": "invoice"},
			Source: extract.Ref{Path: "internal/invoice/invoice.go", Lines: "5-5"},
		},
	}
	rule := func(domain string) reducer.Output {
		return reducer.Output{Domain: domain, DomainSummary: "s", Rules: []reducer.Rule{{
			Slug: "r", Title: "T", EarsKind: "event", Requirement: "QUAND x, le systeme doit y.",
			Citations:          []extract.Ref{{Path: "internal/" + domain + "/x.go", Lines: "1-2"}},
			AcceptanceCriteria: []string{"a", "b"},
		}}}
	}
	nodes := Build(facts, []reducer.Output{rule("billing"), rule("invoice")}, extract.DomainResolver{})
	for _, n := range nodes {
		if n.ID != "domain.invoice" {
			continue
		}
		for _, e := range n.Edges {
			if e.Type == "depends_on" && e.To == "domain.billing" {
				return
			}
		}
		t.Fatalf("domain.invoice should depend on domain.billing via suffix match, edges = %+v", n.Edges)
	}
	t.Fatal("domain.invoice not found")
}

func TestBuildDomainDependsOnFromImports(t *testing.T) {
	nodes := Build(fixtureFacts(), fixtureReduced(), extract.DomainResolver{})
	for _, n := range nodes {
		if n.ID != "domain.http" {
			continue
		}
		for _, e := range n.Edges {
			if e.Type == "depends_on" && e.To == "domain.services" {
				return
			}
		}
		t.Fatalf("domain.http should depend on domain.services, edges = %+v", n.Edges)
	}
	t.Fatal("domain.http not found")
}

func TestBuildIsDeterministic(t *testing.T) {
	a := Build(fixtureFacts(), fixtureReduced(), extract.DomainResolver{})
	b := Build(fixtureFacts(), fixtureReduced(), extract.DomainResolver{})
	if len(a) != len(b) {
		t.Fatalf("node counts differ: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].ID != b[i].ID || a[i].Body != b[i].Body {
			t.Fatalf("node %d differs between runs", i)
		}
	}
}

func TestComputeCoverage(t *testing.T) {
	nodes := Build(fixtureFacts(), fixtureReduced(), extract.DomainResolver{})
	cov := ComputeCoverage(nodes)
	if cov.EndpointsTotal != 1 || cov.EndpointsReferenced != 1 {
		t.Errorf("endpoints coverage = %d/%d, want 1/1", cov.EndpointsReferenced, cov.EndpointsTotal)
	}
	if cov.EntitiesTotal != 1 || cov.EntitiesTouched != 1 {
		t.Errorf("entities coverage = %d/%d, want 1/1", cov.EntitiesTouched, cov.EntitiesTotal)
	}
}
