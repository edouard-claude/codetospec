// Command tuidemo renders the TUI view once with realistic data, for
// documentation screenshots. Not part of the shipped binary.
package main

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"codetospec/internal/llm"
	"codetospec/internal/ui"
)

func main() {
	var model tea.Model = ui.NewModel("old-legacy-app", "./spec-graph", "deepseek-chat", 8, nil)
	msgs := []tea.Msg{
		tea.WindowSizeMsg{Width: 100, Height: 34},
		ui.FactsMerged{Total: 512, ByKind: map[string]int{"symbol": 402, "import": 61, "route": 8, "table": 27, "module": 14}},
		ui.ExtractorFinished{Name: "go", Status: "ok", Facts: 35},
		ui.ExtractorFinished{Name: "scip", Status: "ok", Facts: 402},
		ui.Chunked{Chunks: 460},
	}
	for range 118 {
		msgs = append(msgs, ui.FileExtracted{Path: "service/x.go", Language: "go", Facts: 3})
	}
	// map complete
	for i := 1; i <= 460; i++ {
		msgs = append(msgs, ui.MapUnit{Path: "service/billing.go", Lines: "42-118", Domain: "billing",
			Rules: 2, Done: i, Total: 460, Usage: llm.Usage{PromptTokens: 2500, CompletionTokens: 700}})
	}
	// reduce complete
	msgs = append(msgs, ui.PhaseChanged{Phase: "reduce"})
	for i := 1; i <= 21; i++ {
		msgs = append(msgs, ui.ReduceUnit{Domain: "service", Rules: 12, Done: i, Total: 21,
			Usage: llm.Usage{PromptTokens: 9000, CompletionTokens: 4200}})
	}
	// crosscheck + repair, in progress
	msgs = append(msgs,
		ui.PhaseChanged{Phase: "crosscheck"},
		ui.LogLine{Message: "repaired rule.billing.prorata-refund → cited span 88-140 overlaps func Refund"},
	)
	verdicts := []string{"supported", "repaired", "partial", "unsupported"}
	weights := []int{6, 3, 2, 2} // roughly the observed distribution
	done := 0
	total := 312
	for done < 210 {
		for v, w := range weights {
			for k := 0; k < w && done < 210; k++ {
				done++
				msgs = append(msgs, ui.CrosscheckUnit{RuleID: "rule.x", Verdict: verdicts[v],
					Done: done, Total: total, Usage: llm.Usage{PromptTokens: 1400, CompletionTokens: 190}})
			}
		}
	}

	for _, msg := range msgs {
		model, _ = model.Update(msg)
	}
	fmt.Println(model.View())
}
