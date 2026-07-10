package reducer

import (
	"context"
	"strings"
	"testing"

	"codetospec/internal/extract"
	"codetospec/internal/llm"
	"codetospec/internal/mapper"
)

type chatFunc func(ctx context.Context, msgs []llm.Message) (string, llm.Usage, error)

func (f chatFunc) Chat(ctx context.Context, msgs []llm.Message) (string, llm.Usage, error) {
	return f(ctx, msgs)
}

func candidates() map[string][]mapper.Rule {
	return map[string][]mapper.Rule{
		"billing": {{
			Title:       "Prorata",
			EarsKind:    "event",
			Requirement: "QUAND x, le systeme doit y.",
			Citations:   []extract.Ref{{Path: "app/X.php", Lines: "12-15"}},
			Entities:    []string{"entity.invoices"},
			Confidence:  0.9,
		}},
	}
}

func newReducer(t *testing.T, chat llm.Chatter) *Reducer {
	t.Helper()
	return &Reducer{
		LLM:       chat,
		Lang:      "fr",
		OutDir:    t.TempDir(),
		Entities:  []string{"entity.invoices"},
		Endpoints: []string{"endpoint.post-api-activate"},
	}
}

const validReduceReply = `{"domain_summary": "Facturation.", "rules": [{"slug": "prorata-activation",
"title": "Prorata", "ears_kind": "event", "requirement": "QUAND x, le systeme doit y.",
"rationale": "Vu dans le code.",
"citations": [{"path": "app/X.php", "lines": "12-15"}],
"entities": ["entity.invoices"], "endpoints": [],
"acceptance_criteria": ["a", "b"], "nature": "business", "origin": "explicit", "confidence": 0.9}]}`

func TestReduceNominal(t *testing.T) {
	calls := 0
	r := newReducer(t, chatFunc(func(_ context.Context, msgs []llm.Message) (string, llm.Usage, error) {
		calls++
		if !strings.Contains(msgs[1].Content, "DOMAIN: billing") {
			t.Errorf("user prompt missing domain:\n%s", msgs[1].Content)
		}
		if !strings.Contains(msgs[1].Content, "CANDIDATE_RULES:") {
			t.Error("user prompt missing candidates")
		}
		return validReduceReply, llm.Usage{PromptTokens: 20, CompletionTokens: 10}, nil
	}))

	outputs, err := r.Run(context.Background(), candidates())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if calls != 1 || len(outputs) != 1 {
		t.Fatalf("calls=%d outputs=%d, want 1/1", calls, len(outputs))
	}
	if outputs[0].Failed || len(outputs[0].Rules) != 1 || outputs[0].Rules[0].Slug != "prorata-activation" {
		t.Fatalf("output = %+v", outputs[0])
	}
}

func TestReduceRejectsInventedCitationThenCorrects(t *testing.T) {
	calls := 0
	r := newReducer(t, chatFunc(func(_ context.Context, msgs []llm.Message) (string, llm.Usage, error) {
		calls++
		if calls == 1 {
			// Citation not present verbatim among the candidates.
			return strings.ReplaceAll(validReduceReply, "12-15", "12-16"), llm.Usage{}, nil
		}
		last := msgs[len(msgs)-1]
		if !strings.Contains(last.Content, "output rejected:") {
			t.Errorf("correction message malformed: %q", last.Content)
		}
		return validReduceReply, llm.Usage{}, nil
	}))

	outputs, err := r.Run(context.Background(), candidates())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if calls != 2 || outputs[0].Failed {
		t.Fatalf("calls=%d output=%+v", calls, outputs[0])
	}
}

func TestReduceDoubleFailureMarksDomainFailed(t *testing.T) {
	calls := 0
	r := newReducer(t, chatFunc(func(context.Context, []llm.Message) (string, llm.Usage, error) {
		calls++
		return `{"domain_summary": "x", "rules": [{"slug": "INVALID SLUG"}]}`, llm.Usage{}, nil
	}))

	outputs, err := r.Run(context.Background(), candidates())
	if err != nil {
		t.Fatalf("Run should continue on domain failure, got %v", err)
	}
	if calls != 3 || !outputs[0].Failed {
		t.Fatalf("calls=%d output=%+v, want 3 calls and failed domain", calls, outputs[0])
	}
}

func TestReduceValidationRules(t *testing.T) {
	allowed := map[string]bool{"app/X.php|12-15": true}
	entities := []string{"entity.invoices"}
	endpoints := []string{"endpoint.post-api-activate"}

	duplicated := strings.Replace(validReduceReply, `"rules": [{`, `"rules": [{"slug": "prorata-activation",
"title": "Dup", "ears_kind": "event", "requirement": "QUAND a, le systeme doit b.",
"rationale": "", "citations": [{"path": "app/X.php", "lines": "12-15"}],
"entities": [], "endpoints": [], "acceptance_criteria": ["a", "b"], "nature": "business", "origin": "explicit", "confidence": 0.8}, {`, 1)
	payload, err := validateReduceReply(duplicated, entities, endpoints, allowed)
	if err != nil {
		t.Fatalf("duplicate slug should be repaired, got %v", err)
	}
	if payload.Rules[0].Slug == payload.Rules[1].Slug {
		t.Errorf("duplicate slugs not disambiguated: %q vs %q", payload.Rules[0].Slug, payload.Rules[1].Slug)
	}

	badSlug := strings.ReplaceAll(validReduceReply, "prorata-activation", "Un Slug beaucoup TROP long vraiment")
	payload, err = validateReduceReply(badSlug, entities, endpoints, allowed)
	if err != nil {
		t.Fatalf("malformed slug should be repaired, got %v", err)
	}
	if got := payload.Rules[0].Slug; got != "un-slug-beaucoup-trop-long" {
		t.Errorf("slug repaired to %q, want un-slug-beaucoup-trop-long", got)
	}

	if _, err := validateReduceReply(validReduceReply, entities, endpoints, allowed); err != nil {
		t.Errorf("valid reply rejected: %v", err)
	}
}

func TestReduceResumeSkipsCachedDomains(t *testing.T) {
	r := newReducer(t, chatFunc(func(context.Context, []llm.Message) (string, llm.Usage, error) {
		return validReduceReply, llm.Usage{}, nil
	}))
	if _, err := r.Run(context.Background(), candidates()); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	r.LLM = chatFunc(func(context.Context, []llm.Message) (string, llm.Usage, error) {
		t.Fatal("cached domain must not trigger an LLM call")
		return "", llm.Usage{}, nil
	})
	outputs, err := r.Run(context.Background(), candidates())
	if err != nil || len(outputs) != 1 || outputs[0].Failed {
		t.Fatalf("cached run: outputs=%+v err=%v", outputs, err)
	}
}
