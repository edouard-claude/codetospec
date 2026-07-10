package render

import (
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"codetospec/internal/extract"
	"codetospec/internal/graph"
)

var update = flag.Bool("update", false, "rewrite golden files")

func testNodes() []graph.Node {
	return []graph.Node{
		{
			ID:     "domain.billing",
			Type:   "domain",
			Status: graph.StatusGenerated,
			Title:  "Domaine billing",
			Body:   "Facturation des abonnés.\n\n## Règles\n\n- [Prorata à l'activation](../rules/billing.prorata-activation.md)",
			Edges:  []graph.Edge{{Type: "depends_on", To: "domain.crm"}},
		},
		{
			ID:     "domain.crm",
			Type:   "domain",
			Status: graph.StatusGenerated,
			Title:  "Domaine crm",
			Body:   "Gestion des abonnés.",
		},
		{
			ID:      "entity.invoices",
			Type:    "entity",
			Status:  graph.StatusGenerated,
			Title:   "invoices",
			Body:    "**Table** : `invoices`\n\n**Colonnes** :\n- `id:id`\n- `amount:decimal`",
			Sources: []extract.Ref{{Path: "database/migrations/m.php", Lines: "11-16"}},
		},
		{
			ID:      "endpoint.post-api-activate",
			Type:    "endpoint",
			Status:  graph.StatusGenerated,
			Title:   "POST /api/activate",
			Body:    "**Méthode** : POST\n\n**Path** : `/api/activate`\n\n**Handler** : `App\\Http\\Controllers\\ActivationController@store`",
			Sources: []extract.Ref{{Path: "routes/web.php", Lines: "5-5"}},
		},
		{
			ID:     "rule.billing.prorata-activation",
			Type:   "rule",
			Status: graph.StatusGenerated,
			Title:  "Prorata à l'activation",
			Body: "**Exigence (EARS)** : QUAND un abonné active en cours de mois, le systeme doit facturer au prorata des jours restants.\n\n" +
				"**Justification** : Calcul observé dans ProrataCalculator.\n\n" +
				"**Critères d'acceptation** :\n1. Cas nominal couvert.\n2. Cas limite couvert.\n3. Erreur couverte.",
			Sources: []extract.Ref{{Path: "app/Services/Billing/ProrataCalculator.php", Lines: "11-24"}},
			Edges: []graph.Edge{
				{Type: "belongs_to", To: "domain.billing"},
				{Type: "touches", To: "entity.invoices"},
				{Type: "exposed_by", To: "endpoint.post-api-activate"},
			},
			Extra: map[string]string{"ears": "event", "acceptance": "3", "nature": "business", "origin": "explicit", "confidence": "0.90", "crosscheck": "supported"},
		},
	}
}

func testMeta() Meta {
	return Meta{
		Coverage: graph.Coverage{
			EndpointsTotal: 1, EndpointsReferenced: 1,
			EntitiesTotal: 1, EntitiesTouched: 1,
		},
		ChunksTotal:    5,
		ChunksFailed:   1,
		DomainsTotal:   1,
		FilesNoGrammar: []string{"routes/web.php"},
	}
}

func TestWriteGolden(t *testing.T) {
	outDir := t.TempDir()
	if err := Write(outDir, testNodes(), testMeta()); err != nil {
		t.Fatalf("Write: %v", err)
	}

	goldenFiles := []string{
		"nodes/rules/billing.prorata-activation.md",
		"nodes/domains/billing.md",
		"nodes/entities/invoices.md",
		"nodes/endpoints/post-api-activate.md",
		"graph.json",
		"README.md",
		"llms.txt",
	}
	for _, rel := range goldenFiles {
		got, err := os.ReadFile(filepath.Join(outDir, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("missing output %s: %v", rel, err)
		}
		goldenPath := filepath.Join("testdata", "golden", filepath.FromSlash(rel))
		if *update {
			if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(goldenPath)
		if err != nil {
			t.Fatalf("missing golden %s (run go test ./internal/render -update): %v", goldenPath, err)
		}
		if string(got) != string(want) {
			t.Errorf("%s differs from golden:\n--- got ---\n%s\n--- want ---\n%s", rel, got, want)
		}
	}
}

var mdLink = regexp.MustCompile(`\]\(([^)]+\.md)\)`)

func TestWriteRelativeLinksResolve(t *testing.T) {
	outDir := t.TempDir()
	if err := Write(outDir, testNodes(), testMeta()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	paths, err := filepath.Glob(filepath.Join(outDir, "nodes", "*", "*.md"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no node files written: %v", err)
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range mdLink.FindAllStringSubmatch(string(data), -1) {
			target := filepath.Join(filepath.Dir(path), filepath.FromSlash(m[1]))
			if _, err := os.Stat(target); err != nil {
				t.Errorf("%s: broken relative link %q", path, m[1])
			}
		}
	}
}

func TestParseNodeRoundTrip(t *testing.T) {
	for _, n := range testNodes() {
		parsed, err := ParseNode(NodeMarkdown(n))
		if err != nil {
			t.Fatalf("ParseNode(%s): %v", n.ID, err)
		}
		if parsed.ID != n.ID || parsed.Type != n.Type || parsed.Status != n.Status {
			t.Errorf("%s: id/type/status lost in round-trip", n.ID)
		}
		if parsed.Title != n.Title {
			t.Errorf("%s: title = %q, want %q", n.ID, parsed.Title, n.Title)
		}
		if len(parsed.Sources) != len(n.Sources) || len(parsed.Edges) != len(n.Edges) {
			t.Errorf("%s: sources/edges count lost", n.ID)
		}
		if n.Extra["ears"] != "" && parsed.Extra["ears"] != n.Extra["ears"] {
			t.Errorf("%s: ears = %q, want %q", n.ID, parsed.Extra["ears"], n.Extra["ears"])
		}
	}
}

func TestLoadDir(t *testing.T) {
	outDir := t.TempDir()
	if err := Write(outDir, testNodes(), testMeta()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	nodes, err := LoadDir(outDir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if len(nodes) != len(testNodes()) {
		t.Fatalf("loaded %d nodes, want %d", len(nodes), len(testNodes()))
	}
}

func TestFrontmatterExactShape(t *testing.T) {
	var rule graph.Node
	for _, n := range testNodes() {
		if n.Type == "rule" {
			rule = n
		}
	}
	front := Frontmatter(rule)
	for _, want := range []string{
		"id: rule.billing.prorata-activation\n",
		"type: rule\n",
		"status: generated\n",
		"  - path: app/Services/Billing/ProrataCalculator.php\n    lines: \"11-24\"\n",
		"  - {type: belongs_to, to: domain.billing}\n",
		"  - {type: touches, to: entity.invoices}\n",
		"  - {type: exposed_by, to: endpoint.post-api-activate}\n",
		"ears: event\n",
		"acceptance: 3\n",
	} {
		if !strings.Contains(front, want) {
			t.Errorf("frontmatter missing %q:\n%s", want, front)
		}
	}
}
