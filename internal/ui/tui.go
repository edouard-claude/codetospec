package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"codetospec/internal/llm"
)

// TUISink forwards pipeline events to a running Bubble Tea program.
type TUISink struct {
	Program *tea.Program
}

// Emit implements Sink; tea.Program.Send is safe for concurrent use.
func (s TUISink) Emit(event any) {
	s.Program.Send(event)
}

var (
	headerStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("230")).Background(lipgloss.Color("62")).Padding(0, 1)
	phaseDone     = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
	phaseActive   = lipgloss.NewStyle().Foreground(lipgloss.Color("205")).Bold(true)
	phasePending  = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	labelStyle    = lipgloss.NewStyle().Bold(true).Width(9)
	dimStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	failStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
	okStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	journalStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	footerStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("230")).Background(lipgloss.Color("237")).Padding(0, 1)
	sectionBorder = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("240")).Padding(0, 1)
)

// phase ordering for the sidebar ticks.
var phaseOrder = []string{"extract", "map", "reduce", "build", "crosscheck", "render"}

type tickMsg time.Time

func tickEvery() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// Model is the full-screen run dashboard.
type Model struct {
	src, out, model string
	workers         int
	cancel          func()

	width, height int
	spinner       spinner.Model
	bar           progress.Model
	started       time.Time
	elapsed       time.Duration

	phase       string
	filesWalked int
	lastFile    string
	factsTotal  int
	factsByKind map[string]int
	extractors  []ExtractorFinished
	chunks      int

	mapDone, mapTotal, mapFailed int
	mapRules                     int
	lastChunk                    string
	mapUsage                     llm.Usage

	reduceDone, reduceTotal, reduceFailed int
	reduceRules                           int
	lastDomain                            string
	reduceUsage                           llm.Usage

	checkDone, checkTotal                    int
	checkSupported, checkPartial, checkOther int
	checkUsage                               llm.Usage

	journal  []string
	finished bool
	quitting bool
	result   RunFinished
}

// NewModel builds the dashboard. cancel is invoked when the user quits so
// the pipeline shuts down cleanly (state saved, resumable).
func NewModel(src, out, model string, workers int, cancel func()) Model {
	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	sp.Style = phaseActive
	bar := progress.New(progress.WithDefaultGradient())
	return Model{
		src: src, out: out, model: model, workers: workers,
		cancel:      cancel,
		spinner:     sp,
		bar:         bar,
		started:     time.Now(),
		factsByKind: map[string]int{},
		phase:       "extract",
	}
}

// Result returns the final run event once the program has finished.
func (m Model) Result() (RunFinished, bool) {
	return m.result, m.finished
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, tickEvery())
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			if m.quitting || m.finished {
				return m, tea.Quit
			}
			m.quitting = true
			m.appendJournal("arrêt demandé, sauvegarde de l'état...")
			if m.cancel != nil {
				m.cancel()
			}
		}
		return m, nil

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.bar.Width = max(10, min(msg.Width-30, 60))
		return m, nil

	case tickMsg:
		if !m.finished {
			m.elapsed = time.Since(m.started).Round(time.Second)
		}
		return m, tickEvery()

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case RunStarted:
		m.src, m.out, m.model, m.workers = msg.Src, msg.Out, msg.Model, msg.Workers
		return m, nil

	case FileExtracted:
		m.filesWalked++
		m.lastFile = msg.Path
		return m, nil

	case ExtractorFinished:
		m.extractors = append(m.extractors, msg)
		return m, nil

	case FactsMerged:
		m.factsTotal = msg.Total
		m.factsByKind = msg.ByKind
		return m, nil

	case Chunked:
		m.chunks = msg.Chunks
		return m, nil

	case PhaseChanged:
		m.phase = msg.Phase
		return m, nil

	case MapUnit:
		m.mapDone, m.mapTotal = msg.Done, msg.Total
		if msg.Failed {
			m.mapFailed++
		}
		m.mapRules += msg.Rules
		m.lastChunk = fmt.Sprintf("%s:%s", msg.Path, msg.Lines)
		m.mapUsage.Add(msg.Usage)
		return m, nil

	case ReduceUnit:
		m.reduceDone, m.reduceTotal = msg.Done, msg.Total
		if msg.Failed {
			m.reduceFailed++
		}
		m.reduceRules += msg.Rules
		m.lastDomain = msg.Domain
		m.reduceUsage.Add(msg.Usage)
		return m, nil

	case CrosscheckUnit:
		m.checkDone, m.checkTotal = msg.Done, msg.Total
		switch msg.Verdict {
		case "supported":
			m.checkSupported++
		case "partial":
			m.checkPartial++
		default:
			m.checkOther++
		}
		m.checkUsage.Add(msg.Usage)
		return m, nil

	case LogLine:
		m.appendJournal(msg.Message)
		return m, nil

	case RunFinished:
		m.finished = true
		m.result = msg
		return m, tea.Quit
	}
	return m, nil
}

func (m *Model) appendJournal(line string) {
	m.journal = append(m.journal, line)
	if len(m.journal) > 4 {
		m.journal = m.journal[len(m.journal)-4:]
	}
}

// View implements tea.Model.
func (m Model) View() string {
	if m.width == 0 {
		return "démarrage..."
	}
	var b strings.Builder

	title := fmt.Sprintf("codetospec  %s → %s", m.src, m.out)
	right := fmt.Sprintf("%s · %d workers", m.model, m.workers)
	b.WriteString(headerBar(title, right, m.width) + "\n\n")

	// EXTRACT
	b.WriteString(m.phaseIcon("extract") + labelStyle.Render("extract") + " ")
	extractLine := fmt.Sprintf("%d fichiers · %d facts", m.filesWalked, m.factsTotal)
	if kinds := formatKinds(m.factsByKind); kinds != "" {
		extractLine += dimStyle.Render(" (" + kinds + ")")
	}
	b.WriteString(extractLine + "\n")
	for _, e := range m.extractors {
		status := okStyle.Render("ok")
		if e.Status != "ok" {
			status = failStyle.Render(e.Status)
		}
		b.WriteString("          " + dimStyle.Render(fmt.Sprintf("extracteur %s: ", e.Name)) + status +
			dimStyle.Render(fmt.Sprintf(" · %d facts", e.Facts)) + "\n")
	}
	if m.phase == "extract" && m.lastFile != "" {
		b.WriteString("          " + dimStyle.Render(m.lastFile) + "\n")
	}

	// CHUNK
	b.WriteString(m.phaseIcon("map") + labelStyle.Render("chunk") + " ")
	fmt.Fprintf(&b, "%d chunks\n", m.chunks)

	// MAP
	b.WriteString(m.phaseIcon("map") + labelStyle.Render("map") + " ")
	if m.mapTotal > 0 {
		percent := float64(m.mapDone) / float64(m.mapTotal)
		b.WriteString(m.bar.ViewAs(percent))
		fmt.Fprintf(&b, " %d/%d", m.mapDone, m.mapTotal)
		if m.mapFailed > 0 {
			b.WriteString(failStyle.Render(fmt.Sprintf(" · %d échecs", m.mapFailed)))
		}
		fmt.Fprintf(&b, " · %d règles candidates", m.mapRules)
		b.WriteString("\n")
		if m.phase == "map" && m.lastChunk != "" {
			b.WriteString("          " + m.spinner.View() + dimStyle.Render(m.lastChunk) + "\n")
		}
	} else {
		b.WriteString(dimStyle.Render("en attente") + "\n")
	}

	// REDUCE
	b.WriteString(m.phaseIcon("reduce") + labelStyle.Render("reduce") + " ")
	if m.reduceTotal > 0 {
		fmt.Fprintf(&b, "%d/%d domaines · %d règles finales", m.reduceDone, m.reduceTotal, m.reduceRules)
		if m.reduceFailed > 0 {
			b.WriteString(failStyle.Render(fmt.Sprintf(" · %d échecs", m.reduceFailed)))
		}
		if m.phase == "reduce" {
			b.WriteString("  " + m.spinner.View() + dimStyle.Render(m.lastDomain))
		}
		b.WriteString("\n")
	} else {
		b.WriteString(dimStyle.Render("en attente") + "\n")
	}

	// CROSSCHECK (only shown once the phase produced something)
	if m.checkTotal > 0 {
		b.WriteString(m.phaseIcon("crosscheck") + labelStyle.Render("check") + " ")
		fmt.Fprintf(&b, "%d/%d règles contre-vérifiées · ", m.checkDone, m.checkTotal)
		b.WriteString(okStyle.Render(fmt.Sprintf("%d supported", m.checkSupported)))
		if m.checkPartial > 0 {
			b.WriteString(dimStyle.Render(fmt.Sprintf(" · %d partial", m.checkPartial)))
		}
		if m.checkOther > 0 {
			b.WriteString(failStyle.Render(fmt.Sprintf(" · %d à revoir", m.checkOther)))
		}
		b.WriteString("\n")
	}

	// Journal
	if len(m.journal) > 0 {
		b.WriteString("\n")
		lines := make([]string, len(m.journal))
		for i, l := range m.journal {
			lines[i] = journalStyle.Render(truncateLine(l, m.width-6))
		}
		b.WriteString(sectionBorder.Width(m.width-2).Render(strings.Join(lines, "\n")) + "\n")
	}

	// Footer
	footer := fmt.Sprintf("tokens map %s · reduce %s · total %s   %s   [q] quitter",
		formatUsage(m.mapUsage), formatUsage(m.reduceUsage),
		formatUsage(sumUsage(sumUsage(m.mapUsage, m.reduceUsage), m.checkUsage)), m.elapsed)
	b.WriteString("\n" + footerStyle.Width(m.width).Render(footer))
	return b.String()
}

func headerBar(left, right string, width int) string {
	gap := max(width-lipgloss.Width(left)-lipgloss.Width(right)-4, 1)
	return headerStyle.Width(width).Render(left + strings.Repeat(" ", gap) + right)
}

// phaseIcon renders the status glyph of a phase relative to the current one.
func (m Model) phaseIcon(phase string) string {
	current, target := phaseIndex(m.phase), phaseIndex(phase)
	switch {
	case m.finished || current > target:
		return phaseDone.Render(" ✓ ")
	case current == target:
		return phaseActive.Render(" ● ")
	default:
		return phasePending.Render(" ○ ")
	}
}

func phaseIndex(phase string) int {
	for i, p := range phaseOrder {
		if p == phase {
			return i
		}
	}
	return 0
}

func formatKinds(byKind map[string]int) string {
	if len(byKind) == 0 {
		return ""
	}
	kinds := make([]string, 0, len(byKind))
	for k := range byKind {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	parts := make([]string, len(kinds))
	for i, k := range kinds {
		parts[i] = fmt.Sprintf("%s %d", k, byKind[k])
	}
	return strings.Join(parts, " · ")
}

func formatTokens(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%dk", n/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

func formatUsage(u llm.Usage) string {
	return formatTokens(u.PromptTokens) + "+" + formatTokens(u.CompletionTokens)
}

func sumUsage(a, b llm.Usage) llm.Usage {
	a.Add(b)
	return a
}

func truncateLine(s string, width int) string {
	if width <= 3 || len(s) <= width {
		return s
	}
	return s[:width-3] + "..."
}
