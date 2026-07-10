// Package reducer runs the REDUCE phase: one sequential LLM call per domain,
// consolidating candidate rules into a deduplicated specification.
package reducer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"codetospec/internal/extract"
	"codetospec/internal/llm"
	"codetospec/internal/mapper"
	"codetospec/internal/state"
)

const systemPrompt = `You are a requirements engineer. You consolidate candidate business rules extracted from legacy code into a clean, deduplicated specification for one domain.

Hard rules:
- Output ONLY a valid JSON object matching the schema. No markdown fences, no prose.
- Merge duplicates and near-duplicates. Keep the union of their citations verbatim; never invent or alter citations.
- entities and endpoints MUST be subsets of the provided allowed lists.
- Keep requirements in <LANG>, EARS patterns, one behavior per rule.
- slug: lowercase ascii, words separated by "-", max 5 words, unique within the domain.
- acceptance_criteria: 2 to 5 concrete, testable checks per rule, derived from the requirement, written in <LANG>.
- nature ("business"|"presentation"|"technical") and origin ("explicit"|"implicit"): carry over from the merged candidates; when they disagree, pick the dominant one.
- confidence: number in [0,1], your certainty in the consolidated rule (roughly the max confidence of the candidates you merged).`

const userPromptFormat = `DOMAIN: %s
ALLOWED_ENTITIES: %s
ALLOWED_ENDPOINTS: %s

OUTPUT JSON SCHEMA:
{"domain_summary": string, "rules": [{"slug": string, "title": string, "ears_kind": string, "requirement": string, "rationale": string, "citations": [{"path": string, "lines": "A-B"}], "entities": [string], "endpoints": [string], "acceptance_criteria": [string], "nature": "business|presentation|technical", "origin": "explicit|implicit", "confidence": number}]}

CANDIDATE_RULES:
%s`

// Rule is one consolidated business rule.
type Rule struct {
	Slug               string        `json:"slug"`
	Title              string        `json:"title"`
	EarsKind           string        `json:"ears_kind"`
	Requirement        string        `json:"requirement"`
	Rationale          string        `json:"rationale"`
	Citations          []extract.Ref `json:"citations"`
	Entities           []string      `json:"entities"`
	Endpoints          []string      `json:"endpoints"`
	AcceptanceCriteria []string      `json:"acceptance_criteria"`
	Nature             string        `json:"nature"`
	Origin             string        `json:"origin"`
	Confidence         float64       `json:"confidence"`
}

// Output is the persisted result of reducing one domain.
type Output struct {
	Domain        string `json:"domain"`
	DomainSummary string `json:"domain_summary"`
	Rules         []Rule `json:"rules"`
	Failed        bool   `json:"failed,omitempty"`
	Error         string `json:"error,omitempty"`
}

// reducePayload mirrors the JSON schema requested from the LLM.
type reducePayload struct {
	DomainSummary string `json:"domain_summary"`
	Rules         []Rule `json:"rules"`
}

// Reducer drives the reduce phase.
type Reducer struct {
	LLM       llm.Chatter
	Lang      string
	OutDir    string   // <out>/.codetospec/reduce
	Entities  []string // allowed entity ids
	Endpoints []string // allowed endpoint ids
	// OnUnit is called after each finished domain with its token usage.
	OnUnit func(out Output, usage llm.Usage)
}

// Run reduces every domain sequentially, in sorted domain order, skipping
// domains whose output file already exists.
func (r *Reducer) Run(ctx context.Context, candidates map[string][]mapper.Rule) ([]Output, error) {
	if err := os.MkdirAll(r.OutDir, 0o755); err != nil {
		return nil, fmt.Errorf("create reduce dir: %w", err)
	}
	domains := make([]string, 0, len(candidates))
	for domain := range candidates {
		domains = append(domains, domain)
	}
	sort.Strings(domains)

	var outputs []Output
	for _, domain := range domains {
		if err := ctx.Err(); err != nil {
			return outputs, err
		}
		out, usage, err := r.reduceDomain(ctx, domain, candidates[domain])
		if err != nil {
			return outputs, err
		}
		outputs = append(outputs, out)
		if r.OnUnit != nil {
			r.OnUnit(out, usage)
		}
	}
	return outputs, nil
}

// reduceDomain reduces one domain, reusing a cached output when present.
func (r *Reducer) reduceDomain(ctx context.Context, domain string, rules []mapper.Rule) (Output, llm.Usage, error) {
	outPath := filepath.Join(r.OutDir, domain+".json")
	if data, err := os.ReadFile(outPath); err == nil {
		var cached Output
		if unmarshalErr := json.Unmarshal(data, &cached); unmarshalErr == nil {
			return cached, llm.Usage{}, nil
		}
		slog.Warn("corrupt reduce cache, re-reducing", "path", outPath)
	}

	candidateJSON, err := json.Marshal(rules)
	if err != nil {
		return Output{}, llm.Usage{}, fmt.Errorf("encode candidates: %w", err)
	}
	userPrompt := fmt.Sprintf(userPromptFormat,
		domain, mustJSON(r.Entities), mustJSON(r.Endpoints), string(candidateJSON))
	msgs := []llm.Message{
		{Role: "system", Content: strings.ReplaceAll(systemPrompt, "<LANG>", mapper.LangName(r.Lang))},
		{Role: "user", Content: userPrompt},
	}

	allowedCitations := citationSet(rules)
	var usage llm.Usage
	payload, err := llm.ChatJSON(ctx, r.LLM, msgs, 2, func(u llm.Usage) { usage.Add(u) },
		func(reply string) (reducePayload, error) {
			return validateReduceReply(reply, r.Entities, r.Endpoints, allowedCitations)
		})

	out := Output{Domain: domain}
	switch {
	case err == nil:
		out.DomainSummary = payload.DomainSummary
		out.Rules = payload.Rules
	case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
		return Output{}, usage, err
	default:
		slog.Warn("domain failed", "domain", domain, "err", err)
		out.Failed = true
		out.Error = err.Error()
	}

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return Output{}, usage, fmt.Errorf("encode reduce output: %w", err)
	}
	if err := state.WriteFileAtomic(outPath, append(data, '\n')); err != nil {
		return Output{}, usage, fmt.Errorf("write reduce output: %w", err)
	}
	return out, usage, nil
}

// validateReduceReply enforces the deterministic contract on a reduce reply:
// map-level checks plus slug uniqueness and verbatim citations. Slugs are
// repaired mechanically (slugify, truncate to 5 words, numeric suffix on
// duplicates) instead of burning LLM correction rounds on formatting.
func validateReduceReply(reply string, allowedEntities, allowedEndpoints []string, allowedCitations map[string]bool) (reducePayload, error) {
	var payload reducePayload
	if err := json.Unmarshal([]byte(llm.StripFences(reply)), &payload); err != nil {
		return payload, fmt.Errorf("invalid JSON: %v", err)
	}
	slugs := make(map[string]bool)
	for i := range payload.Rules {
		rule := &payload.Rules[i]
		slug := normalizeSlug(rule.Slug)
		if slug == "" {
			slug = normalizeSlug(rule.Title)
		}
		if slug == "" {
			return payload, fmt.Errorf("rules[%d] has neither a usable slug nor a usable title", i)
		}
		slug = uniqueSlug(slug, slugs)
		slugs[slug] = true
		rule.Slug = slug
	}
	for i, rule := range payload.Rules {
		if !mapper.EARSKinds[rule.EarsKind] {
			return payload, fmt.Errorf("rules[%d].ears_kind %q is not one of ubiquitous|event|state|unwanted|optional", i, rule.EarsKind)
		}
		if len(rule.Citations) == 0 {
			return payload, fmt.Errorf("rules[%d] has no citation", i)
		}
		for _, c := range rule.Citations {
			if _, _, err := extract.ParseLines(c.Lines); err != nil {
				return payload, fmt.Errorf("rules[%d]: %v", i, err)
			}
			if !allowedCitations[citationKey(c)] {
				return payload, fmt.Errorf("rules[%d] cites %s:%s which is not among the candidate citations", i, c.Path, c.Lines)
			}
		}
		if bad := notSubset(rule.Entities, allowedEntities); bad != "" {
			return payload, fmt.Errorf("rules[%d].entities contains %q which is not in ALLOWED_ENTITIES", i, bad)
		}
		if bad := notSubset(rule.Endpoints, allowedEndpoints); bad != "" {
			return payload, fmt.Errorf("rules[%d].endpoints contains %q which is not in ALLOWED_ENDPOINTS", i, bad)
		}
		if !mapper.Natures[rule.Nature] {
			return payload, fmt.Errorf("rules[%d].nature %q is not one of business|presentation|technical", i, rule.Nature)
		}
		if !mapper.Origins[rule.Origin] {
			return payload, fmt.Errorf("rules[%d].origin %q is not one of explicit|implicit", i, rule.Origin)
		}
		if rule.Confidence < 0 || rule.Confidence > 1 {
			return payload, fmt.Errorf("rules[%d].confidence %v is outside [0,1]", i, rule.Confidence)
		}
	}
	return payload, nil
}

// normalizeSlug lowercases, slugifies and truncates a slug to 5 words.
func normalizeSlug(s string) string {
	slug := extract.Slugify(s)
	words := strings.Split(slug, "-")
	if len(words) > 5 {
		words = words[:5]
	}
	return strings.Join(words, "-")
}

// uniqueSlug appends a numeric suffix until slug is unused within the domain.
func uniqueSlug(slug string, taken map[string]bool) string {
	if !taken[slug] {
		return slug
	}
	for n := 2; ; n++ {
		candidate := slug + "-" + strconv.Itoa(n)
		if !taken[candidate] {
			return candidate
		}
	}
}

func citationSet(rules []mapper.Rule) map[string]bool {
	set := make(map[string]bool)
	for _, rule := range rules {
		for _, c := range rule.Citations {
			set[citationKey(c)] = true
		}
	}
	return set
}

func citationKey(c extract.Ref) string {
	return c.Path + "|" + c.Lines
}

// notSubset returns the first element of values missing from allowed.
func notSubset(values, allowed []string) string {
	set := make(map[string]bool, len(allowed))
	for _, a := range allowed {
		set[a] = true
	}
	for _, v := range values {
		if !set[v] {
			return v
		}
	}
	return ""
}

func mustJSON(v any) string {
	data, err := json.Marshal(v)
	if err != nil || string(data) == "null" {
		return "[]"
	}
	return string(data)
}
