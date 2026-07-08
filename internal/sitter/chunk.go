package sitter

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// Chunk is one unit of source code submitted to the map phase.
type Chunk struct {
	ID        string
	Path      string
	StartLine int
	EndLine   int
	Language  string
	Namespace string
	Domain    string
	Content   string
}

const (
	maxDefinitionLines = 300
	fallbackChunkLines = 250
	fallbackOverlap    = 20
)

// ChunkFile splits one file into chunks. A top-level definition becomes one
// chunk when it fits in maxDefinitionLines; larger containers are split per
// child definition, each prefixed with the container header (declaration and
// property lines before the first child). Files without definitions — or
// without a grammar (info == nil) — fall back to fixed-size line windows
// with overlap. domainOf resolves the domain from (namespace, path).
func ChunkFile(path, language string, content []byte, info *FileInfo, domainOf func(namespace, path string) string) []Chunk {
	lines := splitLines(content)
	if len(lines) == 0 {
		return nil
	}

	var modules []Module
	var topDefs []Definition
	var allDefs []Definition
	if info != nil {
		modules = info.Modules
		allDefs = info.Definitions
		topDefs = topLevel(allDefs)
	}

	makeChunk := func(start, end int, chunkContent string) Chunk {
		namespace := namespaceAt(modules, start)
		return Chunk{
			ID:        chunkID(path, start, end, chunkContent),
			Path:      path,
			StartLine: start,
			EndLine:   end,
			Language:  language,
			Namespace: namespace,
			Domain:    domainOf(namespace, path),
			Content:   chunkContent,
		}
	}

	if len(topDefs) == 0 {
		return fallbackChunks(lines, makeChunk)
	}

	var chunks []Chunk
	for _, def := range topDefs {
		size := def.EndLine - def.StartLine + 1
		children := directChildren(allDefs, def)
		if size <= maxDefinitionLines || len(children) == 0 {
			chunks = append(chunks, makeChunk(def.StartLine, def.EndLine, joinLines(lines, def.StartLine, def.EndLine)))
			continue
		}
		header := ""
		if headerEnd := children[0].StartLine - 1; headerEnd >= def.StartLine {
			header = joinLines(lines, def.StartLine, headerEnd)
		}
		for _, child := range children {
			body := joinLines(lines, child.StartLine, child.EndLine)
			if header != "" {
				body = header + "\n" + body
			}
			chunks = append(chunks, makeChunk(child.StartLine, child.EndLine, body))
		}
	}
	sort.SliceStable(chunks, func(i, j int) bool { return chunks[i].StartLine < chunks[j].StartLine })
	return chunks
}

func fallbackChunks(lines []string, makeChunk func(start, end int, content string) Chunk) []Chunk {
	var chunks []Chunk
	total := len(lines)
	for start := 1; start <= total; {
		end := min(start+fallbackChunkLines-1, total)
		chunks = append(chunks, makeChunk(start, end, joinLines(lines, start, end)))
		if end == total {
			break
		}
		start = end - fallbackOverlap + 1
	}
	return chunks
}

// topLevel keeps definitions not contained in any other definition.
func topLevel(defs []Definition) []Definition {
	var top []Definition
	for i, d := range defs {
		contained := false
		for j, other := range defs {
			if i != j && contains(other, d) {
				contained = true
				break
			}
		}
		if !contained {
			top = append(top, d)
		}
	}
	return top
}

// directChildren returns the definitions contained in parent that are not
// themselves contained in another definition inside parent.
func directChildren(defs []Definition, parent Definition) []Definition {
	var inside []Definition
	for _, d := range defs {
		if contains(parent, d) {
			inside = append(inside, d)
		}
	}
	var children []Definition
	for i, d := range inside {
		nested := false
		for j, other := range inside {
			if i != j && contains(other, d) {
				nested = true
				break
			}
		}
		if !nested {
			children = append(children, d)
		}
	}
	sort.SliceStable(children, func(i, j int) bool { return children[i].StartLine < children[j].StartLine })
	return children
}

// contains reports whether outer strictly contains inner.
func contains(outer, inner Definition) bool {
	if outer.StartLine == inner.StartLine && outer.EndLine == inner.EndLine {
		return false
	}
	return outer.StartLine <= inner.StartLine && inner.EndLine <= outer.EndLine
}

// namespaceAt returns the name of the last module declared at or before line.
func namespaceAt(modules []Module, line int) string {
	name := ""
	for _, m := range modules {
		if m.StartLine <= line {
			name = m.Name
		}
	}
	if name == "" && len(modules) > 0 {
		name = modules[0].Name
	}
	return name
}

// splitLines splits content into lines, dropping the empty trailer produced
// by a final newline so line counts match what editors display.
func splitLines(content []byte) []string {
	if len(content) == 0 {
		return nil
	}
	lines := strings.Split(string(content), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// CountLines reports the number of lines in content, consistent with the
// chunker's line numbering.
func CountLines(content []byte) int {
	return len(splitLines(content))
}

func joinLines(lines []string, start, end int) string {
	return strings.Join(lines[start-1:end], "\n")
}

// chunkID derives a stable identifier from the chunk location and content,
// so an edited file is naturally re-mapped.
func chunkID(path string, start, end int, content string) string {
	sum := sha256.Sum256(fmt.Appendf(nil, "%s:%d-%d\n%s", path, start, end, content))
	return hex.EncodeToString(sum[:])[:16]
}
