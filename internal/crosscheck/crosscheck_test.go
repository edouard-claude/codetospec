package crosscheck

import (
	"context"
	"strings"
	"testing"

	"codetospec/internal/extract"
	"codetospec/internal/graph"
	"codetospec/internal/llm"
)

type chatFunc func(ctx context.Context, msgs []llm.Message) (string, llm.Usage, error)

func (f chatFunc) Chat(ctx context.Context, msgs []llm.Message) (string, llm.Usage, error) {
	return f(ctx, msgs)
}

func ruleNode() graph.Node {
	return graph.Node{
		ID:     "rule.services.prorata-activation",
		Type:   "rule",
		Status: graph.StatusGenerated,
		Title:  "Prorata à l'activation",
		Body: "**Exigence (EARS)** : QUAND un abonné active en cours de mois, le systeme doit facturer au prorata.\n\n" +
			"**Critères d'acceptation** :\n1. Cas nominal.\n2. Cas limite.",
		Sources: []extract.Ref{{Path: "app/Services/Billing/ProrataCalculator.php", Lines: "11-24"}},
		Extra:   map[string]string{"ears": "event", "acceptance": "2"},
	}
}

func newChecker(t *testing.T, chat llm.Chatter) *Checker {
	t.Helper()
	return &Checker{
		LLM:     chat,
		Lang:    "fr",
		Workers: 2,
		OutDir:  t.TempDir(),
		SrcRoot: "../../testdata/fixture",
	}
}

func TestCrosscheckNominal(t *testing.T) {
	calls := 0
	c := newChecker(t, chatFunc(func(_ context.Context, msgs []llm.Message) (string, llm.Usage, error) {
		calls++
		if !strings.Contains(msgs[0].Content, "adversarial code reviewer") {
			t.Error("system prompt missing adversarial role")
		}
		user := msgs[1].Content
		if !strings.Contains(user, "RULE: rule.services.prorata-activation") {
			t.Errorf("user prompt missing rule id:\n%s", user)
		}
		// The prompt must carry the real cited source lines, numbered.
		if !strings.Contains(user, "app/Services/Billing/ProrataCalculator.php:11-24") ||
			!strings.Contains(user, "public function calculate") {
			t.Errorf("user prompt missing cited source:\n%s", user)
		}
		return `{"verdict": "supported", "reason": "Le calcul au prorata est visible lignes 21-23."}`, llm.Usage{PromptTokens: 50, CompletionTokens: 10}, nil
	}))

	verdicts, err := c.Run(context.Background(), []graph.Node{ruleNode(), {ID: "domain.x", Type: "domain"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 (domains are not checked)", calls)
	}
	v := verdicts["rule.services.prorata-activation"]
	if v.Verdict != VerdictSupported || v.Reason == "" {
		t.Fatalf("verdict = %+v", v)
	}
}

func TestCrosscheckRejectsBadVerdictThenCorrects(t *testing.T) {
	calls := 0
	c := newChecker(t, chatFunc(func(_ context.Context, msgs []llm.Message) (string, llm.Usage, error) {
		calls++
		if calls == 1 {
			return `{"verdict": "maybe", "reason": "?"}`, llm.Usage{}, nil
		}
		last := msgs[len(msgs)-1]
		if !strings.Contains(last.Content, "output rejected:") {
			t.Errorf("correction message malformed: %q", last.Content)
		}
		return `{"verdict": "partial", "reason": "Le seuil cité diffère de l'exigence."}`, llm.Usage{}, nil
	}))

	verdicts, err := c.Run(context.Background(), []graph.Node{ruleNode()})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if calls != 2 || verdicts["rule.services.prorata-activation"].Verdict != VerdictPartial {
		t.Fatalf("calls=%d verdicts=%+v", calls, verdicts)
	}
}

func TestCrosscheckDoubleFailureIsTraced(t *testing.T) {
	c := newChecker(t, chatFunc(func(context.Context, []llm.Message) (string, llm.Usage, error) {
		return "not json", llm.Usage{}, nil
	}))
	verdicts, err := c.Run(context.Background(), []graph.Node{ruleNode()})
	if err != nil {
		t.Fatalf("Run should continue on failure, got %v", err)
	}
	v := verdicts["rule.services.prorata-activation"]
	if !v.Failed || v.Error == "" {
		t.Fatalf("verdict should be traced as failed: %+v", v)
	}
}

func TestCrosscheckCacheByInputHash(t *testing.T) {
	c := newChecker(t, chatFunc(func(context.Context, []llm.Message) (string, llm.Usage, error) {
		return `{"verdict": "supported", "reason": "ok"}`, llm.Usage{}, nil
	}))
	if _, err := c.Run(context.Background(), []graph.Node{ruleNode()}); err != nil {
		t.Fatal(err)
	}

	// Unchanged rule: cache hit, no LLM call.
	c.LLM = chatFunc(func(context.Context, []llm.Message) (string, llm.Usage, error) {
		t.Fatal("cached verdict must not trigger an LLM call")
		return "", llm.Usage{}, nil
	})
	if _, err := c.Run(context.Background(), []graph.Node{ruleNode()}); err != nil {
		t.Fatal(err)
	}

	// Changed requirement: hash differs, the rule is re-checked.
	recalled := false
	c.LLM = chatFunc(func(context.Context, []llm.Message) (string, llm.Usage, error) {
		recalled = true
		return `{"verdict": "unsupported", "reason": "plus rien ne correspond"}`, llm.Usage{}, nil
	})
	changed := ruleNode()
	changed.Body = strings.ReplaceAll(changed.Body, "au prorata", "en double")
	verdicts, err := c.Run(context.Background(), []graph.Node{changed})
	if err != nil {
		t.Fatal(err)
	}
	if !recalled || verdicts[changed.ID].Verdict != VerdictUnsupported {
		t.Fatalf("changed rule should be re-checked, got %+v", verdicts[changed.ID])
	}
}

func TestAnnotateAndCount(t *testing.T) {
	nodes := []graph.Node{ruleNode(), {ID: "rule.x.failed", Type: "rule", Extra: map[string]string{}}}
	verdicts := map[string]Verdict{
		"rule.services.prorata-activation": {RuleID: "rule.services.prorata-activation", Verdict: VerdictSupported},
		"rule.x.failed":                    {RuleID: "rule.x.failed", Failed: true},
	}
	nodes = Annotate(nodes, verdicts)
	if nodes[0].Extra["crosscheck"] != VerdictSupported {
		t.Errorf("supported rule not annotated: %v", nodes[0].Extra)
	}
	if nodes[1].Extra["crosscheck"] != "" {
		t.Errorf("failed check must not annotate: %v", nodes[1].Extra)
	}
	tally := Count(verdicts)
	if tally.Supported != 1 || tally.Failed != 1 {
		t.Errorf("tally = %+v", tally)
	}
}
