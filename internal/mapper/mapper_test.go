package mapper

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codetospec/internal/llm"
	"codetospec/internal/sitter"
)

// chatFunc adapts a function to the llm.Chatter interface.
type chatFunc func(ctx context.Context, msgs []llm.Message) (string, llm.Usage, error)

func (f chatFunc) Chat(ctx context.Context, msgs []llm.Message) (string, llm.Usage, error) {
	return f(ctx, msgs)
}

func testChunk() sitter.Chunk {
	return sitter.Chunk{
		ID:        "abcdef0123456789",
		Path:      "app/X.php",
		StartLine: 10,
		EndLine:   20,
		Language:  "php",
		Namespace: `App\X`,
		Domain:    "x",
		Content:   "class X {}",
	}
}

func newMapper(t *testing.T, chat llm.Chatter) *Mapper {
	t.Helper()
	return &Mapper{
		LLM:          chat,
		Lang:         "fr",
		Workers:      2,
		OutDir:       t.TempDir(),
		Entities:     []string{"entity.invoices"},
		EndpointsFor: func(string) []string { return []string{"endpoint.post-api-activate"} },
	}
}

const validMapReply = `{"chunk_summary": "un calcul", "rules": [{"title": "T", "ears_kind": "event",
"requirement": "QUAND x, le systeme doit y.",
"citations": [{"path": "app/X.php", "lines": "12-15"}],
"entities": ["entity.invoices"], "endpoints": [], "confidence": 0.9}]}`

func TestMapNominal(t *testing.T) {
	calls := 0
	m := newMapper(t, chatFunc(func(_ context.Context, msgs []llm.Message) (string, llm.Usage, error) {
		calls++
		if len(msgs) != 2 || msgs[0].Role != "system" {
			t.Errorf("unexpected conversation shape: %d messages", len(msgs))
		}
		if !strings.Contains(msgs[1].Content, "FILE: app/X.php (lines 10-20)") {
			t.Errorf("user prompt missing file header:\n%s", msgs[1].Content)
		}
		return validMapReply, llm.Usage{PromptTokens: 10, CompletionTokens: 5}, nil
	}))

	outputs, err := m.Run(context.Background(), []sitter.Chunk{testChunk()})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if calls != 1 || len(outputs) != 1 {
		t.Fatalf("calls=%d outputs=%d, want 1/1", calls, len(outputs))
	}
	if outputs[0].Failed || len(outputs[0].Rules) != 1 {
		t.Fatalf("output = %+v", outputs[0])
	}
	if _, err := os.Stat(filepath.Join(m.OutDir, "abcdef0123456789.json")); err != nil {
		t.Fatalf("map output file not written: %v", err)
	}
}

func TestMapRejectionThenCorrection(t *testing.T) {
	calls := 0
	m := newMapper(t, chatFunc(func(_ context.Context, msgs []llm.Message) (string, llm.Usage, error) {
		calls++
		if calls == 1 {
			// Citation outside the chunk bounds: must be rejected.
			return strings.ReplaceAll(validMapReply, "12-15", "1-5"), llm.Usage{}, nil
		}
		last := msgs[len(msgs)-1]
		if last.Role != "user" || !strings.HasPrefix(last.Content, "output rejected:") ||
			!strings.HasSuffix(last.Content, "resend the full corrected JSON only") {
			t.Errorf("correction message malformed: %q", last.Content)
		}
		return validMapReply, llm.Usage{}, nil
	}))

	outputs, err := m.Run(context.Background(), []sitter.Chunk{testChunk()})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
	if outputs[0].Failed {
		t.Fatalf("output should succeed after correction: %+v", outputs[0])
	}
}

func TestMapDoubleFailureMarksChunkFailed(t *testing.T) {
	calls := 0
	m := newMapper(t, chatFunc(func(context.Context, []llm.Message) (string, llm.Usage, error) {
		calls++
		return "not json at all", llm.Usage{}, nil
	}))

	outputs, err := m.Run(context.Background(), []sitter.Chunk{testChunk()})
	if err != nil {
		t.Fatalf("Run should continue on chunk failure, got %v", err)
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3 (initial + 2 corrections)", calls)
	}
	if !outputs[0].Failed || outputs[0].Error == "" {
		t.Fatalf("output should be failed: %+v", outputs[0])
	}
}

func TestMapResumeSkipsCachedChunks(t *testing.T) {
	m := newMapper(t, chatFunc(func(context.Context, []llm.Message) (string, llm.Usage, error) {
		return validMapReply, llm.Usage{}, nil
	}))
	if _, err := m.Run(context.Background(), []sitter.Chunk{testChunk()}); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	m.LLM = chatFunc(func(context.Context, []llm.Message) (string, llm.Usage, error) {
		t.Fatal("cached chunk must not trigger an LLM call")
		return "", llm.Usage{}, nil
	})
	outputs, err := m.Run(context.Background(), []sitter.Chunk{testChunk()})
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if len(outputs) != 1 || len(outputs[0].Rules) != 1 {
		t.Fatalf("cached output = %+v", outputs)
	}
}

func TestMapValidationRejectsBadValues(t *testing.T) {
	chunk := testChunk()
	cases := map[string]string{
		"bad ears kind":     strings.ReplaceAll(validMapReply, `"event"`, `"whenever"`),
		"foreign path":      strings.ReplaceAll(validMapReply, "app/X.php", "other.php"),
		"unknown entity":    strings.ReplaceAll(validMapReply, "entity.invoices", "entity.ghosts"),
		"confidence out":    strings.ReplaceAll(validMapReply, "0.9", "1.7"),
		"missing citations": strings.ReplaceAll(validMapReply, `[{"path": "app/X.php", "lines": "12-15"}]`, "[]"),
	}
	for name, reply := range cases {
		if _, err := validateMapReply(reply, chunk, []string{"entity.invoices"}, nil); err == nil {
			t.Errorf("%s: validation should fail", name)
		}
	}
}
