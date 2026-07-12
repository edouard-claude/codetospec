package consistency

import (
	"testing"

	"codetospec/internal/graph"
)

func rule(id, requirement string) graph.Node {
	return graph.Node{
		ID:   id,
		Type: "rule",
		Body: "**Exigence (EARS)** : " + requirement,
	}
}

func TestFindDuplicatesFlagsNearIdentical(t *testing.T) {
	nodes := []graph.Node{
		rule("rule.root.note-gol", "Le système doit afficher la note indiquant que les tonnages du Gol incluent Grand Pourpier."),
		rule("rule.usine.note-gol", "Le système doit afficher une note indiquant que les tonnages du Gol incluent Grand Pourpier."),
		rule("rule.billing.prorata", "QUAND un abonné active en cours de mois, le système doit facturer au prorata des jours restants."),
		{ID: "domain.x", Type: "domain", Body: "ignore me"},
	}
	pairs := FindDuplicates(nodes, DefaultThreshold)
	if len(pairs) != 1 {
		t.Fatalf("pairs = %d, want 1 (the two Gol notes)", len(pairs))
	}
	p := pairs[0]
	if p.A != "rule.root.note-gol" || p.B != "rule.usine.note-gol" {
		t.Errorf("pair = %s ↔ %s", p.A, p.B)
	}
	if p.Similarity < DefaultThreshold {
		t.Errorf("similarity = %.2f, want ≥ %.2f", p.Similarity, DefaultThreshold)
	}
}

func TestFindDuplicatesIgnoresDistinctRules(t *testing.T) {
	nodes := []graph.Node{
		rule("rule.a.x", "Le système doit calculer la TVA à vingt pour cent sur le montant hors taxe."),
		rule("rule.b.y", "QUAND un utilisateur se connecte, le système doit vérifier son mot de passe."),
	}
	if pairs := FindDuplicates(nodes, DefaultThreshold); len(pairs) != 0 {
		t.Fatalf("distinct rules should not pair, got %+v", pairs)
	}
}

func TestFindDuplicatesDeterministicOrder(t *testing.T) {
	nodes := []graph.Node{
		rule("rule.z.b", "Le système doit afficher le cumul de la semaine pour Bois Rouge et Le Gol."),
		rule("rule.a.a", "Le système doit afficher le cumul de la semaine pour Bois Rouge et Le Gol."),
	}
	p1 := FindDuplicates(nodes, DefaultThreshold)
	p2 := FindDuplicates(nodes, DefaultThreshold)
	if len(p1) != 1 || len(p2) != 1 || p1[0] != p2[0] {
		t.Fatalf("non-deterministic or wrong: %+v vs %+v", p1, p2)
	}
	// A comes before B lexically.
	if p1[0].A != "rule.a.a" || p1[0].B != "rule.z.b" {
		t.Errorf("pair not ordered by id: %+v", p1[0])
	}
}
