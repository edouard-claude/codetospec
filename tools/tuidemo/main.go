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
	var model tea.Model = ui.NewModel("old-legacy-app", "./spec-graph", "deepseek-chat", 4, nil)
	msgs := []tea.Msg{
		tea.WindowSizeMsg{Width: 96, Height: 30},
		ui.FileExtracted{Path: "index.php", Language: "php", Facts: 2},
		ui.FileExtracted{Path: "app/fonctions.php", Language: "php", Facts: 5},
		ui.ExtractorFinished{Name: "php", Status: "ok", Facts: 57},
		ui.FactsMerged{Total: 69, ByKind: map[string]int{"symbol": 41, "module": 6, "import": 14, "route": 5, "table": 3}},
		ui.Chunked{Chunks: 38},
		ui.PhaseChanged{Phase: "map"},
	}
	for range 22 {
		msgs = append(msgs, ui.FileExtracted{Path: "app/x.php", Language: "php", Facts: 1})
	}
	for i := 1; i <= 25; i++ {
		msgs = append(msgs, ui.MapUnit{
			Path: "app/services/billing.php", Lines: "42-118", Domain: "billing",
			Rules: 2, Done: i, Total: 38,
			Usage: llm.Usage{PromptTokens: 4300, CompletionTokens: 610},
		})
	}
	msgs = append(msgs,
		ui.MapUnit{Path: "app/legacy/report.php", Lines: "1-250", Domain: "reports", Rules: 3, Done: 26, Total: 38,
			Usage: llm.Usage{PromptTokens: 5100, CompletionTokens: 780}},
		ui.LogLine{Message: "parse failed, falling back to line chunks path=assets/style.css"},
		ui.LogLine{Message: "chunk failed chunk=9f31c02a path=app/divers2.php err=rejected after 2 corrections"},
	)
	for _, msg := range msgs {
		model, _ = model.Update(msg)
	}
	fmt.Println(model.View())
}
