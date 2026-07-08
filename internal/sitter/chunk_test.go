package sitter

import (
	"fmt"
	"strings"
	"testing"
)

func noopDomain(namespace, path string) string { return "test" }

func TestChunkFileOneChunkPerDefinition(t *testing.T) {
	content := readFixture(t, "app/Services/Billing/ProrataCalculator.php")
	info, err := Parse("php", content)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	chunks := ChunkFile("app/Services/Billing/ProrataCalculator.php", "php", content, info, noopDomain)
	if len(chunks) != 1 {
		t.Fatalf("chunks = %d, want 1 (one top-level class)", len(chunks))
	}
	c := chunks[0]
	if c.StartLine != 7 || c.EndLine != 25 {
		t.Errorf("chunk lines = %d-%d, want 7-25", c.StartLine, c.EndLine)
	}
	if c.Namespace != `App\Services\Billing` {
		t.Errorf("chunk namespace = %q", c.Namespace)
	}
	if c.Domain != "test" {
		t.Errorf("chunk domain = %q, want test", c.Domain)
	}
	if !strings.HasPrefix(c.Content, "class ProrataCalculator") {
		t.Errorf("chunk content starts with %q", firstLine(c.Content))
	}
	if len(c.ID) != 16 {
		t.Errorf("chunk id %q should be 16 hex chars", c.ID)
	}
}

func TestChunkFileLargeClassSplitsPerMethod(t *testing.T) {
	var b strings.Builder
	b.WriteString("<?php\n\nclass Big\n{\n    public $prop = 1;\n\n")
	for m := 1; m <= 2; m++ {
		fmt.Fprintf(&b, "    public function m%d()\n    {\n", m)
		for range 170 {
			b.WriteString("        $x = 1;\n")
		}
		b.WriteString("    }\n\n")
	}
	b.WriteString("}\n")
	content := []byte(b.String())

	info, err := Parse("php", content)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	chunks := ChunkFile("big.php", "php", content, info, noopDomain)
	if len(chunks) != 2 {
		t.Fatalf("chunks = %d, want 2 (one per method)", len(chunks))
	}
	for _, c := range chunks {
		if !strings.HasPrefix(c.Content, "class Big") {
			t.Errorf("method chunk should start with the class header, got %q", firstLine(c.Content))
		}
		if !strings.Contains(c.Content, "public $prop = 1;") {
			t.Error("method chunk header should include the class properties")
		}
		if !strings.Contains(c.Content, "public function m") {
			t.Error("method chunk should contain the method body")
		}
	}
	if chunks[0].StartLine != 7 {
		t.Errorf("first method chunk starts at %d, want 7", chunks[0].StartLine)
	}
}

func TestChunkFileFallbackWindows(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 600; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	chunks := ChunkFile("data.xyz", "", []byte(b.String()), nil, noopDomain)
	if len(chunks) != 3 {
		t.Fatalf("chunks = %d, want 3", len(chunks))
	}
	wantBounds := [][2]int{{1, 250}, {231, 480}, {461, 600}}
	for i, want := range wantBounds {
		if chunks[i].StartLine != want[0] || chunks[i].EndLine != want[1] {
			t.Errorf("chunk[%d] = %d-%d, want %d-%d", i, chunks[i].StartLine, chunks[i].EndLine, want[0], want[1])
		}
	}
	if !strings.HasSuffix(chunks[0].Content, "line 250") || !strings.HasPrefix(chunks[1].Content, "line 231") {
		t.Error("fallback chunks should overlap by 20 lines")
	}
}

func TestChunkFileEmptyFile(t *testing.T) {
	if chunks := ChunkFile("empty.xyz", "", nil, nil, noopDomain); len(chunks) != 0 {
		t.Fatalf("chunks = %d, want 0", len(chunks))
	}
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
