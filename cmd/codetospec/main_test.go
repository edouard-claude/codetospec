package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"codetospec/internal/config"
	"codetospec/internal/extract"
	"codetospec/internal/llm"
	"codetospec/internal/mapper"
	"codetospec/internal/render"
	"codetospec/internal/ui"
	"codetospec/internal/verify"
)

// mockLLM is a deterministic OpenAI-compatible endpoint: it answers map
// prompts with one rule per chunk containing business code, and reduce
// prompts by consolidating the candidates verbatim.
func mockLLM(t *testing.T) *httptest.Server {
	t.Helper()
	fileHeader := regexp.MustCompile(`FILE: (\S+) \(lines (\d+)-(\d+)\)`)
	listLine := func(prompt, prefix string) []string {
		for line := range strings.SplitSeq(prompt, "\n") {
			if rest, ok := strings.CutPrefix(line, prefix); ok {
				var list []string
				if err := json.Unmarshal([]byte(rest), &list); err != nil {
					t.Errorf("mock: cannot parse %s list %q: %v", prefix, rest, err)
				}
				return list
			}
		}
		return nil
	}

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []llm.Message `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Messages) < 2 {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		system, user := req.Messages[0].Content, req.Messages[1].Content

		var content string
		switch {
		case strings.Contains(system, "software archaeologist"):
			m := fileHeader.FindStringSubmatch(user)
			if m == nil {
				http.Error(w, "no FILE header", http.StatusBadRequest)
				return
			}
			payload := map[string]any{"chunk_summary": "Chunk analysé.", "rules": []any{}}
			if strings.Contains(user, "public function") {
				payload["rules"] = []any{map[string]any{
					"title":       "Règle de " + m[1],
					"ears_kind":   "event",
					"requirement": "QUAND un abonné active en cours de mois, le systeme doit facturer au prorata.",
					"citations":   []any{map[string]any{"path": m[1], "lines": m[2] + "-" + m[3]}},
					"entities":    listLine(user, "ALLOWED_ENTITIES: "),
					"endpoints":   listLine(user, "ALLOWED_ENDPOINTS: "),
					"nature":      "business",
					"origin":      "explicit",
					"confidence":  0.9,
				}}
			}
			data, err := json.Marshal(payload)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			content = string(data)
		case strings.Contains(system, "requirements engineer"):
			_, candidateJSON, ok := strings.Cut(user, "CANDIDATE_RULES:\n")
			if !ok {
				http.Error(w, "no candidates", http.StatusBadRequest)
				return
			}
			var candidates []mapper.Rule
			if err := json.Unmarshal([]byte(candidateJSON), &candidates); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			rules := make([]any, 0, len(candidates))
			for i, c := range candidates {
				rules = append(rules, map[string]any{
					"slug":                "regle-" + string(rune('a'+i)),
					"title":               c.Title,
					"ears_kind":           c.EarsKind,
					"requirement":         c.Requirement,
					"rationale":           "Consolidée depuis le code.",
					"citations":           c.Citations,
					"entities":            c.Entities,
					"endpoints":           c.Endpoints,
					"acceptance_criteria": []string{"Le calcul nominal est correct.", "Le cas limite est couvert."},
					"nature":              "business", "origin": "explicit", "confidence": 0.9,
				})
			}
			data, err := json.Marshal(map[string]any{"domain_summary": "Domaine consolidé.", "rules": rules})
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			content = string(data)
		case strings.Contains(system, "adversarial code reviewer"):
			if !strings.Contains(user, "CITED SOURCE") {
				http.Error(w, "no cited source in crosscheck prompt", http.StatusBadRequest)
				return
			}
			content = `{"verdict": "supported", "reason": "Les lignes citées implémentent la règle."}`
		default:
			http.Error(w, "unknown prompt", http.StatusBadRequest)
			return
		}

		resp := map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": content}}},
			"usage":   map[string]any{"prompt_tokens": 100, "completion_tokens": 50},
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("mock: encode response: %v", err)
		}
	}))
}

func e2eConfig(t *testing.T, serverURL, outDir string) *config.Config {
	t.Helper()
	src, err := filepath.Abs(filepath.Join("..", "..", "testdata", "fixture"))
	if err != nil {
		t.Fatal(err)
	}
	return &config.Config{
		Src:            src,
		Out:            outDir,
		BaseURL:        serverURL,
		Model:          "mock",
		Lang:           "fr",
		Workers:        2,
		MaxTokens:      4096,
		Exclude:        config.DefaultExclude,
		FactsFiles:     []string{filepath.Join(src, "fixture.facts.json")},
		LogLevel:       "warn",
		DomainStrategy: "auto",
		Crosscheck:     true,
	}
}

func TestEndToEndRunOnFixture(t *testing.T) {
	server := mockLLM(t)
	defer server.Close()
	outDir := t.TempDir()
	cfg := e2eConfig(t, server.URL, outDir)

	if err := runPipeline(context.Background(), cfg, ui.Discard{}); err != nil {
		t.Fatalf("runPipeline: %v", err)
	}

	// The graph must be non-empty: >= 1 domain, entity, endpoint, EARS rule.
	nodes, err := render.LoadDir(outDir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	counts := map[string]int{}
	for _, n := range nodes {
		counts[n.Type]++
	}
	for _, nodeType := range []string{"domain", "entity", "endpoint", "rule"} {
		if counts[nodeType] < 1 {
			t.Errorf("no %s node produced (counts=%v)", nodeType, counts)
		}
	}

	// Rendered graph must re-verify cleanly against the sources.
	if violations := verify.Run(nodes, cfg.Src); len(violations) != 0 {
		t.Fatalf("verify violations: %v", violations)
	}

	// The endpoint wired through the facts file must exist and be linked.
	endpointPath := filepath.Join(outDir, "nodes", "endpoints", "post-api-activate.md")
	if _, err := os.Stat(endpointPath); err != nil {
		t.Errorf("endpoint node missing: %v", err)
	}

	// README must contain the Mermaid domain graph and coverage.
	readme, err := os.ReadFile(filepath.Join(outDir, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(readme), "```mermaid") || !strings.Contains(string(readme), "## Couverture") {
		t.Error("README.md missing mermaid graph or coverage section")
	}
	if !strings.Contains(string(readme), "Contre-vérification adversariale") {
		t.Error("README.md missing adversarial crosscheck tally")
	}

	// Every rule node must carry its crosscheck verdict in the frontmatter.
	rulePaths, err := filepath.Glob(filepath.Join(outDir, "nodes", "rules", "*.md"))
	if err != nil || len(rulePaths) == 0 {
		t.Fatalf("no rule files: %v", err)
	}
	ruleData, err := os.ReadFile(rulePaths[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ruleData), "crosscheck: supported") {
		t.Errorf("rule frontmatter missing crosscheck verdict:\n%s", ruleData)
	}

	// graph.json must parse and reference existing files.
	var graphDoc struct {
		Nodes map[string]struct {
			Type  string `json:"type"`
			File  string `json:"file"`
			Edges []any  `json:"edges"`
		} `json:"nodes"`
	}
	graphData, err := os.ReadFile(filepath.Join(outDir, "graph.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(graphData, &graphDoc); err != nil {
		t.Fatalf("graph.json invalid: %v", err)
	}
	if len(graphDoc.Nodes) != len(nodes) {
		t.Errorf("graph.json has %d nodes, want %d", len(graphDoc.Nodes), len(nodes))
	}
	for id, n := range graphDoc.Nodes {
		if _, err := os.Stat(filepath.Join(outDir, filepath.FromSlash(n.File))); err != nil {
			t.Errorf("graph.json node %s references missing file %s", id, n.File)
		}
	}

	// Facts merge must be persisted.
	factsData, err := os.ReadFile(filepath.Join(outDir, ".codetospec", "facts.json"))
	if err != nil {
		t.Fatal(err)
	}
	var envelope extract.FactsEnvelope
	if err := json.Unmarshal(factsData, &envelope); err != nil || envelope.Schema != extract.FactsSchema {
		t.Fatalf("facts.json invalid: %v", err)
	}

	// The verify and stats commands must succeed on the produced graph.
	if code := verifyCommand(&config.Config{Src: cfg.Src, Out: outDir}); code != 0 {
		t.Errorf("verify command exit = %d, want 0", code)
	}
	if code := statsCommand(&config.Config{Out: outDir}); code != 0 {
		t.Errorf("stats command exit = %d, want 0", code)
	}
}

// TestEndToEndDeterministicRerun checks that a second run on a warm cache
// produces byte-for-byte identical output files.
func TestEndToEndDeterministicRerun(t *testing.T) {
	server := mockLLM(t)
	defer server.Close()
	outDir := t.TempDir()
	cfg := e2eConfig(t, server.URL, outDir)

	if err := runPipeline(context.Background(), cfg, ui.Discard{}); err != nil {
		t.Fatalf("first run: %v", err)
	}
	first := snapshotOutput(t, outDir)
	if err := runPipeline(context.Background(), cfg, ui.Discard{}); err != nil {
		t.Fatalf("second run: %v", err)
	}
	second := snapshotOutput(t, outDir)

	if len(first) != len(second) {
		t.Fatalf("file sets differ: %d vs %d", len(first), len(second))
	}
	for path, content := range first {
		if second[path] != content {
			t.Errorf("%s differs between warm-cache runs", path)
		}
	}
}

// snapshotOutput captures every rendered output file (everything except the
// .codetospec cache).
func snapshotOutput(t *testing.T, outDir string) map[string]string {
	t.Helper()
	files := make(map[string]string)
	err := filepath.WalkDir(outDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".codetospec" {
				return filepath.SkipDir
			}
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		rel, relErr := filepath.Rel(outDir, path)
		if relErr != nil {
			return relErr
		}
		files[rel] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}
