package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"codetospec/internal/llm"
)

func drive(t *testing.T, m Model, msgs ...tea.Msg) Model {
	t.Helper()
	var model tea.Model = m
	for _, msg := range msgs {
		model, _ = model.Update(msg)
	}
	result, ok := model.(Model)
	if !ok {
		t.Fatalf("model type changed: %T", model)
	}
	return result
}

func TestModelViewReflectsProgress(t *testing.T) {
	m := NewModel("old-legacy-app", "spec-legacy", "deepseek-chat", 4, nil)
	m = drive(t, m,
		tea.WindowSizeMsg{Width: 100, Height: 30},
		FileExtracted{Path: "index.php", Language: "php", Facts: 3},
		FileExtracted{Path: "app/billing.php", Language: "php", Facts: 5},
		FactsMerged{Total: 12, ByKind: map[string]int{"symbol": 8, "module": 4}},
		Chunked{Chunks: 38},
		PhaseChanged{Phase: "map"},
		MapUnit{Path: "app/billing.php", Lines: "10-80", Domain: "billing", Rules: 2, Done: 1, Total: 38,
			Usage: llm.Usage{PromptTokens: 4000, CompletionTokens: 500}},
		MapUnit{Path: "index.php", Lines: "1-90", Domain: "core", Rules: 1, Done: 2, Total: 38,
			Usage: llm.Usage{PromptTokens: 3000, CompletionTokens: 400}},
		LogLine{Message: "extractor failed name=php"},
	)

	// Header, hero (candidate rules), stage strip, per-domain bars, tokens.
	view := m.View()
	for _, want := range []string{
		"codetospec", "old-legacy-app", "deepseek-chat",
		"CANDIDATE RULES",                   // hero caption
		"2/38",                              // chunks mapped, in the hero subline
		"extract", "map", "reduce", "check", // stage strip
		"CANDIDATES PER DOMAIN", "billing", // domain bars
		"7k+900",   // map tokens: prompt 7000, completion 900
		"[q] quit", // footer hint
	} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}

	// Advance into crosscheck: the verdict stream and proven tally appear.
	m = drive(t, m,
		PhaseChanged{Phase: "reduce"},
		ReduceUnit{Domain: "billing", Rules: 2, Done: 1, Total: 2},
		PhaseChanged{Phase: "crosscheck"},
		CrosscheckUnit{RuleID: "rule.billing.prorata", Verdict: "supported", Done: 1, Total: 2},
		CrosscheckUnit{RuleID: "rule.billing.refund", Verdict: "unsupported", Done: 2, Total: 2},
	)
	view = m.View()
	for _, want := range []string{
		"RULES TO REVIEW",      // hero flips to the review countdown
		"LATEST VERDICTS",      // verdict panel
		"rule.billing.prorata", // a flipped rule id
		"proven",               // tally
		"1 to review",          // the unsupported one
	} {
		if !strings.Contains(view, want) {
			t.Errorf("crosscheck view missing %q:\n%s", want, view)
		}
	}
}

func TestModelQuitPathways(t *testing.T) {
	cancelled := false
	m := NewModel("src", "out", "m", 1, func() { cancelled = true })
	m = drive(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	// First q cancels the pipeline but keeps the screen up.
	m = drive(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if !cancelled {
		t.Fatal("q should cancel the pipeline context")
	}
	if _, finished := m.Result(); finished {
		t.Fatal("model must not be finished before RunFinished")
	}

	// RunFinished quits and exposes the result.
	m = drive(t, m, RunFinished{NodesByType: map[string]int{"rule": 3}})
	result, finished := m.Result()
	if !finished || result.NodesByType["rule"] != 3 {
		t.Fatalf("result = %+v finished=%v", result, finished)
	}
}

func TestFormatTokens(t *testing.T) {
	cases := map[int]string{950: "950", 1500: "1k", 166446: "166k", 2_400_000: "2.4M"}
	for in, want := range cases {
		if got := formatTokens(in); got != want {
			t.Errorf("formatTokens(%d) = %q, want %q", in, got, want)
		}
	}
}
