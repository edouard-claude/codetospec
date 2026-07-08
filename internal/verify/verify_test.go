package verify

import (
	"path/filepath"
	"strings"
	"testing"

	"codetospec/internal/extract"
	"codetospec/internal/graph"
)

var fixtureRoot = filepath.Join("..", "..", "testdata", "fixture")

func validRule() graph.Node {
	return graph.Node{
		ID:     "rule.services.prorata-activation",
		Type:   "rule",
		Status: graph.StatusGenerated,
		Sources: []extract.Ref{
			{Path: "app/Services/Billing/ProrataCalculator.php", Lines: "11-24"},
		},
		Edges: []graph.Edge{{Type: "belongs_to", To: "domain.services"}},
		Title: "Prorata à l'activation",
		Body:  "**Exigence (EARS)** : QUAND x, le systeme doit y.",
		Extra: map[string]string{"ears": "event", "acceptance": "2"},
	}
}

func validDomain() graph.Node {
	return graph.Node{
		ID:     "domain.services",
		Type:   "domain",
		Status: graph.StatusGenerated,
		Title:  "Domaine services",
		Body:   "Résumé.",
	}
}

func TestRunAcceptsValidGraph(t *testing.T) {
	violations := Run([]graph.Node{validRule(), validDomain()}, fixtureRoot)
	if len(violations) != 0 {
		t.Fatalf("violations = %v, want none", violations)
	}
}

func TestRunRejectsDuplicateID(t *testing.T) {
	violations := Run([]graph.Node{validDomain(), validDomain()}, fixtureRoot)
	if !hasCheck(violations, "duplicate-id") {
		t.Fatalf("violations = %v, want duplicate-id", violations)
	}
}

func TestRunRejectsDanglingEdge(t *testing.T) {
	rule := validRule()
	rule.Edges = append(rule.Edges, graph.Edge{Type: "touches", To: "entity.ghost"})
	violations := Run([]graph.Node{rule, validDomain()}, fixtureRoot)
	if !hasCheck(violations, "dangling-edge") {
		t.Fatalf("violations = %v, want dangling-edge", violations)
	}
}

func TestRunRejectsCitationOutOfBounds(t *testing.T) {
	rule := validRule()
	rule.Sources = []extract.Ref{{Path: "app/Services/Billing/ProrataCalculator.php", Lines: "11-999"}}
	violations := Run([]graph.Node{rule, validDomain()}, fixtureRoot)
	if !hasCheck(violations, "unresolvable-citation") {
		t.Fatalf("violations = %v, want unresolvable-citation", violations)
	}
}

func TestRunRejectsMissingCitationPath(t *testing.T) {
	rule := validRule()
	rule.Sources = []extract.Ref{{Path: "app/Nowhere.php", Lines: "1-2"}}
	violations := Run([]graph.Node{rule, validDomain()}, fixtureRoot)
	if !hasCheck(violations, "unresolvable-citation") {
		t.Fatalf("violations = %v, want unresolvable-citation", violations)
	}
}

func TestRunRejectsEscapingPath(t *testing.T) {
	rule := validRule()
	rule.Sources = []extract.Ref{{Path: "../fixture/app/Models/Invoice.php", Lines: "1-2"}}
	violations := Run([]graph.Node{rule, validDomain()}, fixtureRoot)
	if !hasCheck(violations, "unresolvable-citation") {
		t.Fatalf("violations = %v, want unresolvable-citation for escaping path", violations)
	}
}

func TestRunRejectsRuleWithoutCitation(t *testing.T) {
	rule := validRule()
	rule.Sources = nil
	violations := Run([]graph.Node{rule, validDomain()}, fixtureRoot)
	if !hasCheck(violations, "rule-without-citation") {
		t.Fatalf("violations = %v, want rule-without-citation", violations)
	}
}

func TestFrontmatterRoundTrip(t *testing.T) {
	rule := validRule()
	// A title containing markdown-ish characters must survive as well.
	rule.Title = "Prorata: activation *partielle*"
	violations := Run([]graph.Node{rule, validDomain()}, fixtureRoot)
	for _, v := range violations {
		if strings.Contains(v.Check, "round-trip") {
			t.Fatalf("round-trip violation: %v", v)
		}
	}
}

func hasCheck(violations []Violation, check string) bool {
	for _, v := range violations {
		if v.Check == check {
			return true
		}
	}
	return false
}
