// Package crosscheck runs an optional adversarial review pass: for every
// rule node, a fresh-context LLM call receives ONLY the rule and the exact
// source lines it cites, and tries to refute it. Verdicts are recorded,
// never silently dropped — disputed rules stay in the graph, flagged.
//
// The design follows the separation used by large-scale AI porting efforts:
// the model that wrote a claim never judges it; the reviewer sees the diff
// (here: rule + cited code), nothing else.
package crosscheck

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"codetospec/internal/extract"
	"codetospec/internal/graph"
	"codetospec/internal/llm"
	"codetospec/internal/mapper"
	"codetospec/internal/state"
)

// Verdict values, from strongest to weakest support.
const (
	VerdictSupported   = "supported"
	VerdictPartial     = "partial"
	VerdictUnsupported = "unsupported"
)

var verdicts = map[string]bool{
	VerdictSupported:   true,
	VerdictPartial:     true,
	VerdictUnsupported: true,
}

const systemPrompt = `You are an adversarial code reviewer. You receive one candidate business rule and the exact source lines it cites. The rule was written by another model; your job is to try to REFUTE it.

Hard rules:
- Output ONLY a valid JSON object matching the schema provided by the user. No markdown fences, no prose.
- verdict "supported": the cited lines clearly implement the described behavior, including its specific values and thresholds.
- verdict "partial": the behavior is plausible but the cited lines only show part of it, or values/thresholds differ from the requirement.
- verdict "unsupported": the cited lines do not show this behavior at all.
- Judge ONLY from the cited lines. Never assume code you cannot see.
- Write the reason in <LANG>: one or two concrete sentences naming what the lines do or do not show.`

const userPromptFormat = `RULE: %s
TITLE: %s
REQUIREMENT (EARS): %s
ACCEPTANCE_CRITERIA:
%s

CITED SOURCE (verbatim, 1-based line numbers):
%s

OUTPUT JSON SCHEMA:
{"verdict": "supported|partial|unsupported", "reason": string}`

// maxExcerptLines caps how much cited source is sent per rule.
const maxExcerptLines = 400

// Verdict is the persisted result of cross-checking one rule.
type Verdict struct {
	RuleID    string `json:"rule_id"`
	Verdict   string `json:"verdict"`
	Reason    string `json:"reason"`
	InputHash string `json:"input_hash"`
	Failed    bool   `json:"failed,omitempty"`
	Error     string `json:"error,omitempty"`
}

// verdictPayload mirrors the JSON schema requested from the LLM.
type verdictPayload struct {
	Verdict string `json:"verdict"`
	Reason  string `json:"reason"`
}

// Checker drives the adversarial cross-check phase.
type Checker struct {
	LLM     llm.Chatter
	Lang    string
	Workers int
	OutDir  string // <out>/.codetospec/crosscheck
	SrcRoot string
	// OnUnit is called after each finished rule (cached or live).
	OnUnit func(v Verdict, usage llm.Usage)
}

// Run cross-checks every rule node and returns verdicts keyed by rule id.
// Cached verdicts are reused as long as the rule and its cited code are
// unchanged (input hash).
func (c *Checker) Run(ctx context.Context, nodes []graph.Node) (map[string]Verdict, error) {
	if err := os.MkdirAll(c.OutDir, 0o755); err != nil {
		return nil, fmt.Errorf("create crosscheck dir: %w", err)
	}
	var rules []graph.Node
	for _, n := range nodes {
		if n.Type == "rule" {
			rules = append(rules, n)
		}
	}
	sort.SliceStable(rules, func(i, j int) bool { return rules[i].ID < rules[j].ID })

	workers := max(c.Workers, 1)
	jobs := make(chan graph.Node)
	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		out    = make(map[string]Verdict, len(rules))
		runErr error
	)
	record := func(v Verdict, usage llm.Usage, err error) {
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			if runErr == nil {
				runErr = err
			}
			return
		}
		out[v.RuleID] = v
		if c.OnUnit != nil {
			c.OnUnit(v, usage)
		}
	}

	for range workers {
		wg.Go(func() {
			for rule := range jobs {
				v, usage, err := c.checkRule(ctx, rule)
				record(v, usage, err)
			}
		})
	}
feed:
	for _, rule := range rules {
		select {
		case <-ctx.Done():
			break feed
		case jobs <- rule:
		}
	}
	close(jobs)
	wg.Wait()

	if runErr == nil && ctx.Err() != nil {
		runErr = ctx.Err()
	}
	return out, runErr
}

// checkRule reviews one rule, reusing the cached verdict when the rule and
// its cited code are unchanged.
func (c *Checker) checkRule(ctx context.Context, rule graph.Node) (Verdict, llm.Usage, error) {
	excerpt, err := c.citedExcerpt(rule.Sources)
	if err != nil {
		return Verdict{}, llm.Usage{}, fmt.Errorf("rule %s: %w", rule.ID, err)
	}
	hash := inputHash(rule, excerpt)

	outPath := filepath.Join(c.OutDir, rule.ID+".json")
	if data, readErr := os.ReadFile(outPath); readErr == nil {
		var cached Verdict
		if json.Unmarshal(data, &cached) == nil && cached.InputHash == hash {
			return cached, llm.Usage{}, nil
		}
	}

	criteria := "-"
	if lines := acceptanceCriteria(rule.Body); len(lines) > 0 {
		criteria = strings.Join(lines, "\n")
	}
	userPrompt := fmt.Sprintf(userPromptFormat,
		rule.ID, rule.Title, requirementOf(rule.Body), criteria, excerpt)
	msgs := []llm.Message{
		{Role: "system", Content: strings.ReplaceAll(systemPrompt, "<LANG>", mapper.LangName(c.Lang))},
		{Role: "user", Content: userPrompt},
	}

	var usage llm.Usage
	payload, err := llm.ChatJSON(ctx, c.LLM, msgs, 2, func(u llm.Usage) { usage.Add(u) },
		func(reply string) (verdictPayload, error) {
			var p verdictPayload
			if unmarshalErr := json.Unmarshal([]byte(llm.StripFences(reply)), &p); unmarshalErr != nil {
				return p, fmt.Errorf("invalid JSON: %v", unmarshalErr)
			}
			if !verdicts[p.Verdict] {
				return p, fmt.Errorf("verdict %q is not one of supported|partial|unsupported", p.Verdict)
			}
			if strings.TrimSpace(p.Reason) == "" {
				return p, errors.New("reason must not be empty")
			}
			return p, nil
		})

	v := Verdict{RuleID: rule.ID, InputHash: hash}
	switch {
	case err == nil:
		v.Verdict = payload.Verdict
		v.Reason = payload.Reason
	case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
		return Verdict{}, usage, err
	default:
		slog.Warn("crosscheck failed", "rule", rule.ID, "err", err)
		v.Failed = true
		v.Error = err.Error()
	}

	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return Verdict{}, usage, fmt.Errorf("encode verdict: %w", err)
	}
	if err := state.WriteFileAtomic(outPath, append(data, '\n')); err != nil {
		return Verdict{}, usage, fmt.Errorf("write verdict: %w", err)
	}
	return v, usage, nil
}

// citedExcerpt reads the exact cited lines from the source tree, with
// 1-based line numbers, capped at maxExcerptLines.
func (c *Checker) citedExcerpt(sources []extract.Ref) (string, error) {
	var b strings.Builder
	budget := maxExcerptLines
	for _, src := range sources {
		start, end, err := extract.ParseLines(src.Lines)
		if err != nil {
			return "", err
		}
		data, err := os.ReadFile(filepath.Join(c.SrcRoot, filepath.FromSlash(src.Path)))
		if err != nil {
			return "", fmt.Errorf("read cited file: %w", err)
		}
		lines := strings.Split(string(data), "\n")
		if end > len(lines) {
			end = len(lines)
		}
		fmt.Fprintf(&b, "--- %s:%s ---\n", src.Path, src.Lines)
		for i := start; i <= end && budget > 0; i++ {
			fmt.Fprintf(&b, "%5d | %s\n", i, lines[i-1])
			budget--
		}
		if budget == 0 {
			b.WriteString("... (excerpt truncated)\n")
			break
		}
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// Annotate writes verdicts into the rule nodes (Extra["crosscheck"]) so the
// frontmatter and reports carry them. Failed checks are left unannotated.
func Annotate(nodes []graph.Node, verdictsByRule map[string]Verdict) []graph.Node {
	for i := range nodes {
		v, ok := verdictsByRule[nodes[i].ID]
		if !ok || v.Failed {
			continue
		}
		if nodes[i].Extra == nil {
			nodes[i].Extra = map[string]string{}
		}
		nodes[i].Extra["crosscheck"] = v.Verdict
	}
	return nodes
}

// Tally counts verdicts for reporting.
type Tally struct {
	Supported   int
	Partial     int
	Unsupported int
	Failed      int
}

// Count tallies a verdict set.
func Count(verdictsByRule map[string]Verdict) Tally {
	var t Tally
	for _, v := range verdictsByRule {
		switch {
		case v.Failed:
			t.Failed++
		case v.Verdict == VerdictSupported:
			t.Supported++
		case v.Verdict == VerdictPartial:
			t.Partial++
		case v.Verdict == VerdictUnsupported:
			t.Unsupported++
		}
	}
	return t
}

func inputHash(rule graph.Node, excerpt string) string {
	sum := sha256.Sum256(fmt.Appendf(nil, "%s\n%s\n%s\n%s", rule.ID, rule.Title, rule.Body, excerpt))
	return hex.EncodeToString(sum[:])[:16]
}

// requirementOf extracts the EARS requirement line from a rule body.
func requirementOf(body string) string {
	for line := range strings.SplitSeq(body, "\n") {
		if rest, ok := strings.CutPrefix(line, "**Exigence (EARS)** : "); ok {
			return rest
		}
	}
	first, _, _ := strings.Cut(body, "\n")
	return first
}

// acceptanceCriteria extracts the numbered criteria lines from a rule body.
func acceptanceCriteria(body string) []string {
	var criteria []string
	inList := false
	for line := range strings.SplitSeq(body, "\n") {
		if strings.HasPrefix(line, "**Critères d'acceptation**") {
			inList = true
			continue
		}
		if inList {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || !(trimmed[0] >= '0' && trimmed[0] <= '9') {
				break
			}
			criteria = append(criteria, trimmed)
		}
	}
	return criteria
}
