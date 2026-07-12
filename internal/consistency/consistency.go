// Package consistency runs deterministic cross-rule checks: it flags
// near-duplicate rules (the same behavior surfaced in several places) so a
// reviewer can reconcile them. No LLM; pure text similarity.
package consistency

import (
	"regexp"
	"sort"
	"strings"

	"codetospec/internal/graph"
)

// DuplicatePair is two rules whose requirements are near-identical.
type DuplicatePair struct {
	A, B       string  // rule ids, A < B
	Similarity float64 // Jaccard over requirement word sets, in [0,1]
}

// DefaultThreshold is the similarity above which two rules are reported as
// duplicate candidates.
const DefaultThreshold = 0.8

// FindDuplicates returns near-duplicate rule pairs, most similar first.
// Deterministic: pairs and ordering are stable.
func FindDuplicates(nodes []graph.Node, threshold float64) []DuplicatePair {
	if threshold <= 0 {
		threshold = DefaultThreshold
	}
	type rule struct {
		id    string
		words map[string]bool
	}
	var rules []rule
	for _, n := range nodes {
		if n.Type != "rule" {
			continue
		}
		words := tokenize(requirementOf(n.Body))
		if len(words) == 0 {
			continue
		}
		rules = append(rules, rule{id: n.ID, words: words})
	}
	sort.SliceStable(rules, func(i, j int) bool { return rules[i].id < rules[j].id })

	var pairs []DuplicatePair
	for i := range rules {
		for j := i + 1; j < len(rules); j++ {
			sim := jaccard(rules[i].words, rules[j].words)
			if sim >= threshold {
				pairs = append(pairs, DuplicatePair{A: rules[i].id, B: rules[j].id, Similarity: sim})
			}
		}
	}
	sort.SliceStable(pairs, func(i, j int) bool {
		if pairs[i].Similarity != pairs[j].Similarity {
			return pairs[i].Similarity > pairs[j].Similarity
		}
		if pairs[i].A != pairs[j].A {
			return pairs[i].A < pairs[j].A
		}
		return pairs[i].B < pairs[j].B
	})
	return pairs
}

var wordRe = regexp.MustCompile(`[\p{L}\p{N}]+`)

// tokenize lowercases and extracts the word set of a requirement, dropping
// very short tokens (articles, glue words) that inflate similarity.
func tokenize(s string) map[string]bool {
	words := make(map[string]bool)
	for _, w := range wordRe.FindAllString(strings.ToLower(s), -1) {
		if len([]rune(w)) >= 3 {
			words[w] = true
		}
	}
	return words
}

func jaccard(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for w := range a {
		if b[w] {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
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
