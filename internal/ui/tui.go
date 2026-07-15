package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

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

// Palette — a dark "work queue" look: near-black ground, cream accent, and
// amber/violet/green stage colors that read as a pipeline.
const (
	cream  = lipgloss.Color("#fbf0df")
	amber  = lipgloss.Color("#fbbf24")
	violet = lipgloss.Color("#a78bfa")
	green  = lipgloss.Color("#4ade80")
	red    = lipgloss.Color("#f87171")
	ink    = lipgloss.Color("#e5e7eb")
	gray   = lipgloss.Color("#9ca3af")
	dim    = lipgloss.Color("#6b7280")
	line   = lipgloss.Color("#374151")
	ground = lipgloss.Color("#0b0c10")
)

// domainHues cycles per-domain bar colors, like the per-crate bars.
var domainHues = []lipgloss.Color{
	"#f472b6", "#fb923c", "#f87171", "#34d399", "#38bdf8",
	"#fbbf24", "#a78bfa", "#22d3ee", "#a3e635", "#fb7185",
}

var (
	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(cream).Background(ground).Padding(0, 1)
	brandStyle  = lipgloss.NewStyle().Foreground(amber).Bold(true)
	rightStyle  = lipgloss.NewStyle().Foreground(gray)

	capStyle   = lipgloss.NewStyle().Foreground(dim).Bold(true)
	heroNum    = lipgloss.NewStyle().Foreground(cream).Bold(true)
	heroDone   = lipgloss.NewStyle().Foreground(green).Bold(true)
	heroSub    = lipgloss.NewStyle().Foreground(gray)
	clockStyle = lipgloss.NewStyle().Foreground(cream)

	dimStyle  = lipgloss.NewStyle().Foreground(dim)
	grayStyle = lipgloss.NewStyle().Foreground(gray)
	okStyle   = lipgloss.NewStyle().Foreground(green)
	okBold    = lipgloss.NewStyle().Foreground(green).Bold(true)
	amberSt   = lipgloss.NewStyle().Foreground(amber)
	failStyle = lipgloss.NewStyle().Foreground(red).Bold(true)

	panelStyle  = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(line).Padding(0, 1)
	footerStyle = lipgloss.NewStyle().Foreground(gray).Background(ground).Padding(0, 1)
	trackStyle  = lipgloss.NewStyle().Foreground(line)
)

// stages drives both the pipeline strip and the phase ticks.
var stages = []struct{ key, label string }{
	{"extract", "extract"},
	{"map", "map"},
	{"reduce", "reduce"},
	{"crosscheck", "check"},
	{"render", "render"},
}

var phaseOrder = []string{"extract", "map", "reduce", "build", "crosscheck", "render"}

type tickMsg time.Time

func tickEvery() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
}

type checkItem struct {
	id      string
	verdict string
}

// Model is the full-screen run dashboard.
type Model struct {
	src, out, model string
	workers         int
	cancel          func()

	width, height int
	spinner       spinner.Model
	started       time.Time
	elapsed       time.Duration
	pulse         bool

	phase       string
	filesWalked int
	lastFile    string
	factsTotal  int
	factsByKind map[string]int
	extractors  []ExtractorFinished
	chunks      int

	mapDone, mapTotal, mapFailed int
	mapRules                     int
	mapByDomain                  map[string]int
	lastChunk                    string
	mapUsage                     llm.Usage

	reduceDone, reduceTotal, reduceFailed int
	reduceRules                           int
	reduceByDomain                        map[string]int
	lastDomain                            string
	reduceUsage                           llm.Usage

	checkDone, checkTotal                                   int
	checkSupported, checkRepaired, checkPartial, checkOther int
	recentChecks                                            []checkItem
	checkUsage                                              llm.Usage

	journal  []string
	finished bool
	quitting bool
	result   RunFinished
}

// NewModel builds the dashboard. cancel is invoked when the user quits so
// the pipeline shuts down cleanly (state saved, resumable).
func NewModel(src, out, model string, workers int, cancel func()) Model {
	sp := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	sp.Style = lipgloss.NewStyle().Foreground(amber)
	return Model{
		src: src, out: out, model: model, workers: workers,
		cancel:         cancel,
		spinner:        sp,
		started:        time.Now(),
		factsByKind:    map[string]int{},
		mapByDomain:    map[string]int{},
		reduceByDomain: map[string]int{},
		phase:          "extract",
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
			m.appendJournal("stopping, saving state...")
			if m.cancel != nil {
				m.cancel()
			}
		}
		return m, nil

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tickMsg:
		if !m.finished {
			m.elapsed = time.Since(m.started).Round(time.Second)
		}
		m.pulse = !m.pulse
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
		if msg.Domain != "" {
			m.mapByDomain[msg.Domain] += msg.Rules
		}
		m.lastChunk = fmt.Sprintf("%s:%s", msg.Path, msg.Lines)
		m.mapUsage.Add(msg.Usage)
		return m, nil

	case ReduceUnit:
		m.reduceDone, m.reduceTotal = msg.Done, msg.Total
		if msg.Failed {
			m.reduceFailed++
		}
		m.reduceRules += msg.Rules
		if msg.Domain != "" {
			m.reduceByDomain[msg.Domain] += msg.Rules
		}
		m.lastDomain = msg.Domain
		m.reduceUsage.Add(msg.Usage)
		return m, nil

	case CrosscheckUnit:
		m.checkDone, m.checkTotal = msg.Done, msg.Total
		switch msg.Verdict {
		case "supported":
			m.checkSupported++
		case "repaired":
			m.checkRepaired++
		case "partial":
			m.checkPartial++
		default:
			m.checkOther++
		}
		m.recentChecks = append(m.recentChecks, checkItem{id: msg.RuleID, verdict: msg.Verdict})
		if len(m.recentChecks) > 6 {
			m.recentChecks = m.recentChecks[len(m.recentChecks)-6:]
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
	if len(m.journal) > 3 {
		m.journal = m.journal[len(m.journal)-3:]
	}
}

// View implements tea.Model.
func (m Model) View() string {
	if m.width == 0 {
		return "starting..."
	}
	var b strings.Builder

	// Header
	left := brandStyle.Render("codetospec") + rightStyle.Render(fmt.Sprintf("  %s → %s", m.src, m.out))
	right := rightStyle.Render(fmt.Sprintf("%s · %d workers", m.model, m.workers))
	b.WriteString(headerBar(left, right, m.width) + "\n\n")

	// Hero metric + clock
	b.WriteString(m.hero() + "\n\n")

	// Pipeline stage strip
	b.WriteString(m.stageStrip() + "\n")

	// Per-domain bars — the "rules land per domain" panel.
	if bars := m.domainBars(); bars != "" {
		b.WriteString("\n" + bars + "\n")
	}

	// Verdict stream — rules flipping proven, like errors.txt → ✓.
	if len(m.recentChecks) > 0 {
		b.WriteString("\n" + m.verdictStream() + "\n")
	}

	// Journal
	if len(m.journal) > 0 {
		lines := make([]string, len(m.journal))
		for i, l := range m.journal {
			lines[i] = dimStyle.Render("· " + truncateLine(l, m.width-8))
		}
		b.WriteString("\n" + strings.Join(lines, "\n") + "\n")
	}

	// Footer
	total := sumUsage(sumUsage(m.mapUsage, m.reduceUsage), m.checkUsage)
	footer := fmt.Sprintf("tokens  map %s · reduce %s · total %s      %s      %s",
		formatUsage(m.mapUsage), formatUsage(m.reduceUsage), formatUsage(total),
		m.elapsed, quitHint(m))
	b.WriteString("\n" + footerStyle.Width(m.width).Render(footer))
	return b.String()
}

func quitHint(m Model) string {
	if m.finished {
		return "done — [q] close"
	}
	if m.quitting {
		return "saving…"
	}
	return "[q] quit"
}

// hero renders the phase-appropriate headline metric, big and bright, with a
// live clock on the right — the "0 errors left" of the work-queue view.
func (m Model) hero() string {
	var n int
	var cap, sub string
	numStyle := heroNum
	proven := m.checkSupported + m.checkRepaired
	toReview := m.checkPartial + m.checkOther

	switch {
	case m.checkTotal > 0:
		n, cap = toReview, "RULES TO REVIEW"
		sub = okStyle.Render(fmt.Sprintf("%d proven", proven)) +
			grayStyle.Render(fmt.Sprintf("  ·  %d/%d checked", m.checkDone, m.checkTotal))
		if toReview == 0 && m.checkDone >= m.checkTotal && m.checkTotal > 0 {
			numStyle, cap = heroDone, "ALL RULES PROVEN"
		} else if toReview == 0 {
			numStyle = heroDone
		}
	case m.phase == "reduce" || m.reduceRules > 0:
		n, cap = m.reduceRules, "RULES CONSOLIDATED"
		sub = grayStyle.Render(fmt.Sprintf("%d/%d domains", m.reduceDone, m.reduceTotal))
	case m.phase == "map" || m.mapRules > 0:
		n, cap = m.mapRules, "CANDIDATE RULES"
		sub = grayStyle.Render(fmt.Sprintf("%d/%d chunks mapped", m.mapDone, m.mapTotal))
	default:
		n, cap = m.factsTotal, "FACTS EXTRACTED"
		sub = grayStyle.Render(fmt.Sprintf("%d files", m.filesWalked))
	}

	numTxt := numStyle.Render(bigNum(n))
	head := capStyle.Render(cap) + "   " + heroSub.Render(sub)
	left := head + "\n  " + numTxt

	clock := capStyle.Render("ELAPSED") + "\n" + clockStyle.Render(fmt.Sprintf("%8s", m.elapsed))
	gap := max(m.width-lipgloss.Width(left)-lipgloss.Width(clock)-2, 1)
	rows := lipgloss.JoinHorizontal(lipgloss.Top, "  "+left, strings.Repeat(" ", gap), clock)
	return rows
}

// bigNum groups digits for readability (1,204).
func bigNum(n int) string {
	s := fmt.Sprintf("%d", n)
	if n < 1000 {
		return s
	}
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	return string(out)
}

// stageStrip renders the pipeline as colored cells that pulse on the active
// stage — extract ■ → map ■ → reduce ■ → check ▢ → render ▢.
func (m Model) stageStrip() string {
	cur := phaseIndex(m.phase)
	var cells []string
	for _, st := range stages {
		ti := phaseIndex(st.key)
		var sq, name lipgloss.Style
		var glyph string
		switch {
		case m.finished || cur > ti:
			glyph = "■"
			sq = lipgloss.NewStyle().Foreground(green)
			name = lipgloss.NewStyle().Foreground(ink)
		case cur == ti:
			glyph = "■"
			c := amber
			if !m.pulse {
				c = cream
			}
			sq = lipgloss.NewStyle().Foreground(c).Bold(true)
			name = lipgloss.NewStyle().Foreground(cream).Bold(true)
		default:
			glyph = "▢"
			sq = lipgloss.NewStyle().Foreground(dim)
			name = lipgloss.NewStyle().Foreground(dim)
		}
		cells = append(cells, sq.Render(glyph)+" "+name.Render(st.label))
	}
	return "  " + strings.Join(cells, trackStyle.Render("  →  "))
}

// domainBars renders one horizontal bar per domain — the analog of the
// per-crate commit bars. Uses reduce counts once present, else map candidates.
func (m Model) domainBars() string {
	src := m.reduceByDomain
	title := "RULES PER DOMAIN"
	if len(src) == 0 {
		src, title = m.mapByDomain, "CANDIDATES PER DOMAIN"
	}
	if len(src) == 0 {
		return ""
	}
	type dc struct {
		name string
		n    int
	}
	items := make([]dc, 0, len(src))
	maxN, total := 1, 0
	for k, v := range src {
		items = append(items, dc{k, v})
		total += v
		if v > maxN {
			maxN = v
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].n != items[j].n {
			return items[i].n > items[j].n
		}
		return items[i].name < items[j].name
	})

	nameW := 16
	track := max(min(m.width-nameW-14, 46), 10)
	const rowsShown = 7
	var rows []string
	for i, it := range items {
		if i >= rowsShown {
			break
		}
		hue := domainHues[i%len(domainHues)]
		filled := max(it.n*track/maxN, 1)
		bar := lipgloss.NewStyle().Foreground(hue).Render(strings.Repeat("█", filled)) +
			trackStyle.Render(strings.Repeat("░", track-filled))
		name := grayStyle.Render(fmt.Sprintf("%*s", nameW, truncateLine(it.name, nameW)))
		cnt := lipgloss.NewStyle().Foreground(ink).Render(fmt.Sprintf("%5s", bigNum(it.n)))
		rows = append(rows, fmt.Sprintf("%s  %s %s", name, bar, cnt))
	}
	head := capStyle.Render(title)
	if len(items) > rowsShown {
		head += grayStyle.Render(fmt.Sprintf("   +%d more · %s total", len(items)-rowsShown, bigNum(total)))
	} else {
		head += grayStyle.Render(fmt.Sprintf("   %s total", bigNum(total)))
	}
	return panelStyle.Width(m.width - 6).Render(head + "\n" + strings.Join(rows, "\n"))
}

// verdictStream shows the latest reviewed rules flipping to a verdict, plus a
// colored tally — the errors.txt → ✓ column.
func (m Model) verdictStream() string {
	var rows []string
	for _, c := range m.recentChecks {
		var glyph string
		var st lipgloss.Style
		switch c.verdict {
		case "supported", "repaired":
			glyph, st = "✓", okStyle
		case "partial":
			glyph, st = "≈", amberSt
		default:
			glyph, st = "✗", failStyle
		}
		id := truncateLine(c.id, max(m.width-16, 20))
		rows = append(rows, st.Render(glyph)+" "+grayStyle.Render(id))
	}
	tally := okBold.Render(fmt.Sprintf("%d proven", m.checkSupported+m.checkRepaired))
	if m.checkRepaired > 0 {
		tally += grayStyle.Render(fmt.Sprintf(" (%d repaired)", m.checkRepaired))
	}
	if m.checkPartial > 0 {
		tally += amberSt.Render(fmt.Sprintf("  ·  %d partial", m.checkPartial))
	}
	if m.checkOther > 0 {
		tally += failStyle.Render(fmt.Sprintf("  ·  %d to review", m.checkOther))
	}
	head := capStyle.Render("LATEST VERDICTS") + "   " + tally
	return panelStyle.Width(m.width - 6).Render(head + "\n" + strings.Join(rows, "\n"))
}

func headerBar(left, right string, width int) string {
	gap := max(width-lipgloss.Width(left)-lipgloss.Width(right)-2, 1)
	return headerStyle.Width(width).Render(left + strings.Repeat(" ", gap) + right)
}

func phaseIndex(phase string) int {
	// map non-strip phases onto the strip: build folds into reduce's slot.
	if phase == "build" {
		phase = "reduce"
	}
	for i, p := range phaseOrder {
		if p == phase {
			return i
		}
	}
	return 0
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
	return s[:width-1] + "…"
}
