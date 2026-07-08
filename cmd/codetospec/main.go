// Command codetospec reads a source repository, extracts business rules and
// produces a versionable markdown knowledge graph.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"

	"codetospec/internal/config"
	"codetospec/internal/extract"
	"codetospec/internal/graph"
	"codetospec/internal/llm"
	"codetospec/internal/mapper"
	"codetospec/internal/reducer"
	"codetospec/internal/render"
	"codetospec/internal/sitter"
	"codetospec/internal/source"
	"codetospec/internal/state"
	"codetospec/internal/ui"
	"codetospec/internal/verify"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

const usageText = `usage:
  codetospec run --src <repo-dir> --out <graph-dir> [flags]
  codetospec verify --src <repo-dir> --out <graph-dir>
  codetospec stats --out <graph-dir>`

func run(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, usageText)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch args[0] {
	case "run":
		cfg, err := config.ParseRun(args[1:])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		if useTUI(cfg) {
			return runTUI(ctx, cfg)
		}
		setupLogging(cfg.LogLevel)
		if err := runPipeline(ctx, cfg, ui.Plain{}); err != nil {
			if errors.Is(err, context.Canceled) {
				slog.Warn("interrupted, state saved; rerun to resume")
				return 130
			}
			slog.Error("run failed", "err", err)
			return 1
		}
		return 0
	case "verify":
		cfg, err := config.ParseVerify(args[1:])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		setupLogging(cfg.LogLevel)
		return verifyCommand(cfg)
	case "stats":
		cfg, err := config.ParseStats(args[1:])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		setupLogging(cfg.LogLevel)
		return statsCommand(cfg)
	default:
		fmt.Fprintln(os.Stderr, usageText)
		return 2
	}
}

func setupLogging(level string) {
	var l slog.Level
	if err := l.UnmarshalText([]byte(level)); err != nil {
		l = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: l})))
}

// useTUI enables the dashboard on an interactive terminal unless --no-tui.
func useTUI(cfg *config.Config) bool {
	if cfg.NoTUI {
		return false
	}
	info, err := os.Stdout.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// runTUI runs the pipeline behind the full-screen Bubble Tea dashboard.
func runTUI(ctx context.Context, cfg *config.Config) int {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	program := tea.NewProgram(
		ui.NewModel(cfg.Src, cfg.Out, cfg.Model, cfg.Workers, cancel),
		tea.WithAltScreen(),
	)
	sink := ui.TUISink{Program: program}
	slog.SetDefault(slog.New(ui.LogHandler{Sink: sink, Min: slog.LevelWarn}))

	go func() {
		// runPipeline emits RunFinished itself, which quits the program.
		_ = runPipeline(runCtx, cfg, sink)
	}()

	finalModel, err := program.Run()
	setupLogging(cfg.LogLevel)
	if err != nil {
		slog.Error("tui failed", "err", err)
		return 1
	}
	model, ok := finalModel.(ui.Model)
	if !ok {
		return 1
	}
	result, finished := model.Result()
	if !finished {
		// User quit before the pipeline reported; state is saved.
		fmt.Println("interrompu, état sauvegardé ; relancer pour reprendre")
		return 130
	}
	switch {
	case errors.Is(result.Err, context.Canceled):
		fmt.Println("interrompu, état sauvegardé ; relancer pour reprendre")
		return 130
	case result.Err != nil:
		fmt.Fprintln(os.Stderr, "run failed:", result.Err)
		for _, v := range result.Violations {
			fmt.Fprintln(os.Stderr, " -", v)
		}
		return 1
	default:
		ui.PrintSummary(result)
		return 0
	}
}

// runPipeline is the full run sequence: extract, chunk, map, reduce, build,
// verify, render. Progress is reported through the sink, which also receives
// a final RunFinished event (consumed by the TUI and the plain summary).
func runPipeline(ctx context.Context, cfg *config.Config, sink ui.Sink) (err error) {
	final := ui.RunFinished{Extractors: map[string]string{}}
	defer func() {
		final.Err = err
		sink.Emit(final)
	}()
	sink.Emit(ui.RunStarted{Src: cfg.Src, Out: cfg.Out, Model: cfg.Model, Workers: cfg.Workers})

	resolver := extract.DomainResolver{Strategy: cfg.DomainStrategy}

	// Walk and extract layer A (tree-sitter).
	files, err := source.Walk(cfg.Src, cfg.Exclude)
	if err != nil {
		return err
	}
	var sitterFacts []extract.Fact
	fileData := make(map[string][]byte, len(files))
	fileInfo := make(map[string]*sitter.FileInfo)
	var noGrammar []string
	for _, f := range files {
		data, readErr := os.ReadFile(f.AbsPath)
		if readErr != nil {
			slog.Warn("unreadable file skipped", "path", f.Path, "err", readErr)
			continue
		}
		fileData[f.Path] = data
		if f.Language == "" {
			noGrammar = append(noGrammar, f.Path)
			sink.Emit(ui.FileExtracted{Path: f.Path})
			continue
		}
		info, parseErr := sitter.Parse(f.Language, data)
		if parseErr != nil {
			slog.Warn("parse failed, falling back to line chunks", "path", f.Path, "err", parseErr)
			noGrammar = append(noGrammar, f.Path)
			sink.Emit(ui.FileExtracted{Path: f.Path, Language: f.Language})
			continue
		}
		fileInfo[f.Path] = info
		facts := extract.UniversalFacts(f.Path, info)
		sitterFacts = append(sitterFacts, facts...)
		sink.Emit(ui.FileExtracted{Path: f.Path, Language: f.Language, Facts: len(facts)})
	}
	slog.Info("extract: universal layer done", "files", len(files), "facts", len(sitterFacts))

	// Layer B: external extractors (graceful degradation).
	extractorStatus := map[string]string{}
	var extractorFacts []extract.Fact
	for _, e := range cfg.Extractors {
		timeout, timeoutErr := e.TimeoutDuration()
		if timeoutErr != nil {
			return timeoutErr
		}
		facts, extErr := extract.RunExtractor(ctx, extract.ExtractorConfig{
			Name:    e.Name,
			Cmd:     e.Cmd,
			Args:    e.Args,
			Timeout: timeout,
		}, cfg.Src)
		if extErr != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			slog.Warn("extractor failed", "name", e.Name, "err", extErr)
			extractorStatus[e.Name] = "failed"
			sink.Emit(ui.ExtractorFinished{Name: e.Name, Status: "failed"})
			continue
		}
		extractorStatus[e.Name] = "ok"
		extractorFacts = append(extractorFacts, facts...)
		sink.Emit(ui.ExtractorFinished{Name: e.Name, Status: "ok", Facts: len(facts)})
	}
	final.Extractors = extractorStatus

	// Layer C: facts files.
	var fileFacts []extract.Fact
	for _, path := range cfg.FactsFiles {
		facts, loadErr := extract.LoadFactsFile(path)
		if loadErr != nil {
			return loadErr
		}
		fileFacts = append(fileFacts, facts...)
	}

	facts := extract.Merge(sitterFacts, extractorFacts, fileFacts)
	byKind := map[string]int{}
	for _, f := range facts {
		byKind[f.Kind]++
	}
	sink.Emit(ui.FactsMerged{Total: len(facts), ByKind: byKind})
	if err := writeFactsFile(cfg.Out, facts); err != nil {
		return err
	}

	// State store.
	st, err := state.Open(filepath.Join(cfg.Out, ".codetospec", "state.json"), cfg.Src)
	if err != nil {
		return err
	}
	if err := st.Update(func(s *state.State) {
		maps.Copy(s.Extractors, extractorStatus)
	}); err != nil {
		return err
	}

	// Chunk.
	var chunks []sitter.Chunk
	for _, f := range files {
		data, ok := fileData[f.Path]
		if !ok {
			continue
		}
		chunks = append(chunks, sitter.ChunkFile(f.Path, f.Language, data, fileInfo[f.Path], resolver.Resolve)...)
	}
	sort.SliceStable(chunks, func(i, j int) bool {
		if chunks[i].Path != chunks[j].Path {
			return chunks[i].Path < chunks[j].Path
		}
		return chunks[i].StartLine < chunks[j].StartLine
	})
	sink.Emit(ui.Chunked{Chunks: len(chunks)})

	entities, endpointsByPath := allowedLists(facts)
	client := llm.NewClient(cfg.BaseURL, cfg.APIKey, cfg.Model, cfg.MaxTokens)

	// MAP.
	sink.Emit(ui.PhaseChanged{Phase: "map"})
	if err := st.Update(func(s *state.State) {
		s.Phase = "map"
		s.ChunksTotal = len(chunks)
		s.ChunksDone = 0
		s.ChunksFailed = 0
	}); err != nil {
		return err
	}
	m := &mapper.Mapper{
		LLM:      client,
		Lang:     cfg.Lang,
		Workers:  cfg.Workers,
		OutDir:   filepath.Join(cfg.Out, ".codetospec", "map"),
		Entities: entities,
		EndpointsFor: func(path string) []string {
			return endpointsByPath[path]
		},
		OnUnit: func(out mapper.Output, usage llm.Usage) {
			var done int
			if err := st.Update(func(s *state.State) {
				s.ChunksDone++
				if out.Failed {
					s.ChunksFailed++
				}
				s.Tokens["map"].Prompt += usage.PromptTokens
				s.Tokens["map"].Completion += usage.CompletionTokens
				done = s.ChunksDone
			}); err != nil {
				slog.Warn("state save failed", "err", err)
			}
			sink.Emit(ui.MapUnit{
				Path:   out.Path,
				Lines:  fmt.Sprintf("%d-%d", out.StartLine, out.EndLine),
				Domain: out.Domain,
				Rules:  len(out.Rules),
				Failed: out.Failed,
				Done:   done,
				Total:  len(chunks),
				Usage:  usage,
			})
		},
	}
	mapOutputs, err := m.Run(ctx, chunks)
	if err != nil {
		return err
	}

	// REDUCE.
	candidates := make(map[string][]mapper.Rule)
	for _, out := range mapOutputs {
		if !out.Failed && len(out.Rules) > 0 {
			candidates[out.Domain] = append(candidates[out.Domain], out.Rules...)
		}
	}
	allEndpoints := allEndpointIDs(facts)
	sink.Emit(ui.PhaseChanged{Phase: "reduce"})
	if err := st.Update(func(s *state.State) {
		s.Phase = "reduce"
		s.DomainsTotal = len(candidates)
		s.DomainsDone = 0
		s.DomainsFailed = 0
	}); err != nil {
		return err
	}
	r := &reducer.Reducer{
		LLM:       client,
		Lang:      cfg.Lang,
		OutDir:    filepath.Join(cfg.Out, ".codetospec", "reduce"),
		Entities:  entities,
		Endpoints: allEndpoints,
		OnUnit: func(out reducer.Output, usage llm.Usage) {
			var done int
			if err := st.Update(func(s *state.State) {
				s.DomainsDone++
				if out.Failed {
					s.DomainsFailed++
				}
				s.Tokens["reduce"].Prompt += usage.PromptTokens
				s.Tokens["reduce"].Completion += usage.CompletionTokens
				done = s.DomainsDone
			}); err != nil {
				slog.Warn("state save failed", "err", err)
			}
			sink.Emit(ui.ReduceUnit{
				Domain: out.Domain,
				Rules:  len(out.Rules),
				Failed: out.Failed,
				Done:   done,
				Total:  len(candidates),
				Usage:  usage,
			})
		},
	}
	reduced, err := r.Run(ctx, candidates)
	if err != nil {
		return err
	}

	// BUILD + VERIFY + RENDER.
	sink.Emit(ui.PhaseChanged{Phase: "build"})
	if err := st.Update(func(s *state.State) { s.Phase = "build" }); err != nil {
		return err
	}
	nodes := graph.Build(facts, reduced, resolver)
	violations := verify.Run(nodes, cfg.Src)
	if len(violations) > 0 {
		for _, v := range violations {
			final.Violations = append(final.Violations, v.String())
			slog.Error("verification failed", "node", v.NodeID, "check", v.Check, "detail", v.Detail)
		}
		return fmt.Errorf("graph verification failed with %d violation(s), nothing rendered", len(violations))
	}

	snapshot := st.Snapshot()
	meta := render.Meta{
		Coverage:       graph.ComputeCoverage(nodes),
		ChunksFailed:   snapshot.ChunksFailed,
		ChunksTotal:    snapshot.ChunksTotal,
		DomainsFailed:  snapshot.DomainsFailed,
		DomainsTotal:   snapshot.DomainsTotal,
		FilesNoGrammar: noGrammar,
	}
	for name, status := range extractorStatus {
		if status != "ok" {
			meta.ExtractorsFailed = append(meta.ExtractorsFailed, name)
		}
	}
	sink.Emit(ui.PhaseChanged{Phase: "render"})
	if err := render.Write(cfg.Out, nodes, meta); err != nil {
		return err
	}
	if err := st.Update(func(s *state.State) { s.Phase = "done" }); err != nil {
		return err
	}

	nodesByType := map[string]int{}
	for _, n := range nodes {
		nodesByType[n.Type]++
	}
	final.NodesByType = nodesByType
	final.Coverage = meta.Coverage
	final.ChunksFailed = snapshot.ChunksFailed
	final.ChunksTotal = snapshot.ChunksTotal
	final.DomainsFailed = snapshot.DomainsFailed
	final.DomainsTotal = snapshot.DomainsTotal
	if t := snapshot.Tokens["map"]; t != nil {
		final.TokensMap = llm.Usage{PromptTokens: t.Prompt, CompletionTokens: t.Completion}
	}
	if t := snapshot.Tokens["reduce"]; t != nil {
		final.TokensReduce = llm.Usage{PromptTokens: t.Prompt, CompletionTokens: t.Completion}
	}
	return nil
}

// writeFactsFile persists the merged facts under <out>/.codetospec/facts.json.
func writeFactsFile(outDir string, facts []extract.Fact) error {
	data, err := json.MarshalIndent(extract.FactsEnvelope{Schema: extract.FactsSchema, Facts: facts}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode facts.json: %w", err)
	}
	return state.WriteFileAtomic(filepath.Join(outDir, ".codetospec", "facts.json"), append(data, '\n'))
}

// allowedLists derives the allowed entity ids and, per source file, the
// endpoint ids whose controller/handler references a symbol of that file.
func allowedLists(facts []extract.Fact) (entities []string, endpointsByPath map[string][]string) {
	for _, f := range facts {
		if f.Kind == "table" {
			entities = append(entities, graph.EntityID(f))
		}
	}
	sort.Strings(entities)
	entities = dedup(entities)

	type symbol struct {
		path string
		fqn  string
		name string
	}
	var symbols []symbol
	for _, f := range facts {
		if f.Kind != "symbol" {
			continue
		}
		name := f.Attrs["name"]
		fqn := name
		if ns := f.Attrs["namespace"]; ns != "" {
			fqn = ns + "\\" + name
		}
		symbols = append(symbols, symbol{path: f.Source.Path, fqn: fqn, name: name})
	}

	endpointsByPath = make(map[string][]string)
	for _, f := range facts {
		if f.Kind != "route" {
			continue
		}
		target := f.Attrs["controller"]
		if target == "" {
			target = f.Attrs["handler"]
		}
		if target == "" {
			continue
		}
		id := graph.EndpointID(f)
		for _, s := range symbols {
			if strings.Contains(target, s.fqn) {
				endpointsByPath[s.path] = append(endpointsByPath[s.path], id)
			}
		}
	}
	for path := range endpointsByPath {
		sort.Strings(endpointsByPath[path])
		endpointsByPath[path] = dedup(endpointsByPath[path])
	}
	return entities, endpointsByPath
}

func allEndpointIDs(facts []extract.Fact) []string {
	var ids []string
	for _, f := range facts {
		if f.Kind == "route" {
			ids = append(ids, graph.EndpointID(f))
		}
	}
	sort.Strings(ids)
	return dedup(ids)
}

func dedup(sorted []string) []string {
	var out []string
	for i, s := range sorted {
		if i == 0 || s != sorted[i-1] {
			out = append(out, s)
		}
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func verifyCommand(cfg *config.Config) int {
	nodes, err := render.LoadDir(cfg.Out)
	if err != nil {
		slog.Error("cannot load graph", "err", err)
		return 1
	}
	if len(nodes) == 0 {
		slog.Error("no nodes found", "out", cfg.Out)
		return 1
	}
	violations := verify.Run(nodes, cfg.Src)
	if len(violations) > 0 {
		for _, v := range violations {
			fmt.Fprintln(os.Stderr, v.String())
		}
		return 1
	}
	fmt.Printf("ok: %d nodes verified\n", len(nodes))
	return 0
}

func statsCommand(cfg *config.Config) int {
	s, err := state.Load(filepath.Join(cfg.Out, ".codetospec", "state.json"))
	if err != nil {
		slog.Error("cannot load state", "err", err)
		return 1
	}
	fmt.Printf("phase: %s (started %s)\n", s.Phase, s.StartedAt)
	fmt.Printf("chunks: %d done, %d failed, %d total\n", s.ChunksDone, s.ChunksFailed, s.ChunksTotal)
	fmt.Printf("domains: %d done, %d failed, %d total\n", s.DomainsDone, s.DomainsFailed, s.DomainsTotal)
	for _, phase := range []string{"map", "reduce"} {
		if t := s.Tokens[phase]; t != nil {
			fmt.Printf("tokens %s: prompt %d, completion %d\n", phase, t.Prompt, t.Completion)
		}
	}
	for _, name := range sortedKeys(s.Extractors) {
		fmt.Printf("extractor %s: %s\n", name, s.Extractors[name])
	}
	return 0
}
