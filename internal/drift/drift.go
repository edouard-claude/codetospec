// Package drift detects when the code a rule cites has changed since the
// spec was generated. Each rule carries a digest of its cited source; the
// drift command recomputes it against the current tree and flags mismatches.
// Fully deterministic, no LLM.
package drift

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"codetospec/internal/extract"
	"codetospec/internal/graph"
)

// Status classifies a rule against the current source tree.
type Status string

const (
	StatusOK      Status = "ok"      // cited code is unchanged
	StatusStale   Status = "stale"   // cited code changed
	StatusBroken  Status = "broken"  // a citation no longer resolves
	StatusUnknown Status = "unknown" // rule has no stored digest
)

// RuleStatus is the drift result for one rule.
type RuleStatus struct {
	RuleID string
	Status Status
	Detail string
}

// Report is the outcome of a drift check.
type Report struct {
	Rules   []RuleStatus
	OK      int
	Stale   int
	Broken  int
	Unknown int
}

// Drifted reports whether any rule is stale or broken.
func (r Report) Drifted() bool { return r.Stale > 0 || r.Broken > 0 }

// Digest hashes the exact cited source lines of a set of references, so an
// edit inside any cited span changes the digest. Sources are sorted for a
// stable result. Returns an error only for an unreadable file or bad range.
func Digest(srcRoot string, sources []extract.Ref) (string, error) {
	ordered := append([]extract.Ref(nil), sources...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Path != ordered[j].Path {
			return ordered[i].Path < ordered[j].Path
		}
		return ordered[i].Lines < ordered[j].Lines
	})
	h := sha256.New()
	for _, src := range ordered {
		start, end, err := extract.ParseLines(src.Lines)
		if err != nil {
			return "", fmt.Errorf("%s: %w", src.Path, err)
		}
		data, err := os.ReadFile(filepath.Join(srcRoot, filepath.FromSlash(src.Path)))
		if err != nil {
			return "", fmt.Errorf("read %s: %w", src.Path, err)
		}
		lines := strings.Split(string(data), "\n")
		if end > len(lines) {
			return "", fmt.Errorf("%s: lines %s exceed file (%d lines)", src.Path, src.Lines, len(lines))
		}
		fmt.Fprintf(h, "%s|%s\n%s\n", src.Path, src.Lines, strings.Join(lines[start-1:end], "\n"))
	}
	return hex.EncodeToString(h.Sum(nil))[:16], nil
}

// Check recomputes each rule's digest against the current source and
// compares it to the digest stored in the rule node.
func Check(nodes []graph.Node, srcRoot string) Report {
	var report Report
	for _, n := range nodes {
		if n.Type != "rule" {
			continue
		}
		stored := n.Extra["digest"]
		rs := RuleStatus{RuleID: n.ID}
		switch {
		case stored == "":
			rs.Status = StatusUnknown
			rs.Detail = "no stored digest (regenerate the spec to enable drift checks)"
			report.Unknown++
		default:
			current, err := Digest(srcRoot, n.Sources)
			switch {
			case err != nil:
				rs.Status = StatusBroken
				rs.Detail = err.Error()
				report.Broken++
			case current != stored:
				rs.Status = StatusStale
				rs.Detail = "cited code changed since generation"
				report.Stale++
			default:
				rs.Status = StatusOK
				report.OK++
			}
		}
		report.Rules = append(report.Rules, rs)
	}
	return report
}
