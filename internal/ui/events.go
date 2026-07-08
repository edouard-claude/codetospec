// Package ui carries run progress events from the pipeline to a consumer:
// either the plain slog output or the full-screen Bubble Tea TUI.
package ui

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"codetospec/internal/graph"
	"codetospec/internal/llm"
)

// Sink receives pipeline progress events. Implementations must be safe for
// concurrent use (map workers emit in parallel).
type Sink interface {
	Emit(event any)
}

// RunStarted opens a run.
type RunStarted struct {
	Src     string
	Out     string
	Model   string
	Workers int
}

// FileExtracted reports one walked file (layer A).
type FileExtracted struct {
	Path     string
	Language string // "" when no grammar matches
	Facts    int
}

// ExtractorFinished reports one external extractor (layer B).
type ExtractorFinished struct {
	Name   string
	Status string // "ok" | "failed"
	Facts  int
}

// FactsMerged reports the merged fact set.
type FactsMerged struct {
	Total  int
	ByKind map[string]int
}

// Chunked reports the chunking result.
type Chunked struct {
	Chunks int
}

// PhaseChanged reports a pipeline phase transition.
type PhaseChanged struct {
	Phase string // "extract" | "map" | "reduce" | "build" | "render"
}

// MapUnit reports one mapped chunk (cached or live).
type MapUnit struct {
	Path   string
	Lines  string
	Domain string
	Rules  int
	Failed bool
	Done   int
	Total  int
	Usage  llm.Usage
}

// ReduceUnit reports one reduced domain.
type ReduceUnit struct {
	Domain string
	Rules  int
	Failed bool
	Done   int
	Total  int
	Usage  llm.Usage
}

// LogLine forwards a log record (warnings surface in the TUI journal).
type LogLine struct {
	Level   slog.Level
	Message string
}

// RunFinished closes a run, successfully or not.
type RunFinished struct {
	Err           error
	NodesByType   map[string]int
	Coverage      graph.Coverage
	Violations    []string
	ChunksFailed  int
	ChunksTotal   int
	DomainsFailed int
	DomainsTotal  int
	Extractors    map[string]string
	TokensMap     llm.Usage
	TokensReduce  llm.Usage
}

// PrintSummary prints the end-of-run summary on stdout (plain mode, and
// after the TUI exits the alternate screen).
func PrintSummary(e RunFinished) {
	fmt.Printf("nodes: %d domains, %d entities, %d endpoints, %d rules\n",
		e.NodesByType["domain"], e.NodesByType["entity"], e.NodesByType["endpoint"], e.NodesByType["rule"])
	fmt.Printf("coverage: endpoints %d/%d, entities %d/%d\n",
		e.Coverage.EndpointsReferenced, e.Coverage.EndpointsTotal,
		e.Coverage.EntitiesTouched, e.Coverage.EntitiesTotal)
	fmt.Printf("failures: %d/%d chunks, %d/%d domains\n",
		e.ChunksFailed, e.ChunksTotal, e.DomainsFailed, e.DomainsTotal)
	fmt.Printf("tokens: map %d+%d, reduce %d+%d (prompt+completion)\n",
		e.TokensMap.PromptTokens, e.TokensMap.CompletionTokens,
		e.TokensReduce.PromptTokens, e.TokensReduce.CompletionTokens)
	if len(e.Extractors) > 0 {
		names := make([]string, 0, len(e.Extractors))
		for name := range e.Extractors {
			names = append(names, name)
		}
		sort.Strings(names)
		parts := make([]string, len(names))
		for i, name := range names {
			parts[i] = name + "=" + e.Extractors[name]
		}
		fmt.Printf("extractors: %s\n", strings.Join(parts, " "))
	}
}

// Discard drops every event; used by tests.
type Discard struct{}

// Emit implements Sink.
func (Discard) Emit(any) {}

// Plain logs events through slog, preserving the original non-TUI output.
type Plain struct{}

// Emit implements Sink.
func (Plain) Emit(event any) {
	switch e := event.(type) {
	case FactsMerged:
		slog.Info("extract done", "facts", e.Total)
	case Chunked:
		slog.Info("chunking done", "chunks", e.Chunks)
	case ExtractorFinished:
		slog.Info("extractor finished", "name", e.Name, "status", e.Status, "facts", e.Facts)
	case MapUnit:
		if e.Done%25 == 0 {
			slog.Info("map progress", "done", e.Done, "total", e.Total)
		}
	case ReduceUnit:
		slog.Info("reduce progress", "domain", e.Domain, "done", e.Done, "total", e.Total)
	case LogLine:
		slog.Log(context.Background(), e.Level, e.Message)
	case RunFinished:
		if e.Err == nil {
			PrintSummary(e)
		}
	}
}
