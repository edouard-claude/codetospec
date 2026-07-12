package drift

import (
	"os"
	"path/filepath"
	"testing"

	"codetospec/internal/extract"
	"codetospec/internal/graph"
)

func writeSource(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func ruleWithDigest(t *testing.T, root string, src extract.Ref) graph.Node {
	t.Helper()
	digest, err := Digest(root, []extract.Ref{src})
	if err != nil {
		t.Fatalf("Digest: %v", err)
	}
	return graph.Node{
		ID:      "rule.x.r",
		Type:    "rule",
		Sources: []extract.Ref{src},
		Extra:   map[string]string{"digest": digest},
	}
}

func TestCheckDetectsStaleAndOK(t *testing.T) {
	dir := t.TempDir()
	writeSource(t, dir, "calc.go", "package x\n\nfunc f() int {\n\treturn 1\n}\n")
	rule := ruleWithDigest(t, dir, extract.Ref{Path: "calc.go", Lines: "3-5"})

	// Unchanged -> ok.
	if r := Check([]graph.Node{rule}, dir); r.OK != 1 || r.Drifted() {
		t.Fatalf("unchanged rule should be ok, got %+v", r)
	}

	// Edit inside the cited span -> stale.
	writeSource(t, dir, "calc.go", "package x\n\nfunc f() int {\n\treturn 2\n}\n")
	r := Check([]graph.Node{rule}, dir)
	if r.Stale != 1 || !r.Drifted() {
		t.Fatalf("edited cited code should be stale, got %+v", r)
	}
	if r.Rules[0].Status != StatusStale {
		t.Errorf("status = %q, want stale", r.Rules[0].Status)
	}
}

func TestCheckEditOutsideCitedSpanStaysOK(t *testing.T) {
	dir := t.TempDir()
	writeSource(t, dir, "calc.go", "package x\n\nfunc f() int {\n\treturn 1\n}\n\nfunc g() {}\n")
	rule := ruleWithDigest(t, dir, extract.Ref{Path: "calc.go", Lines: "3-5"})

	// Change a line OUTSIDE the cited span (line 7) -> still ok.
	writeSource(t, dir, "calc.go", "package x\n\nfunc f() int {\n\treturn 1\n}\n\nfunc g() { println(1) }\n")
	if r := Check([]graph.Node{rule}, dir); r.OK != 1 || r.Drifted() {
		t.Fatalf("edit outside cited span must not drift, got %+v", r)
	}
}

func TestCheckBrokenAndUnknown(t *testing.T) {
	dir := t.TempDir()
	writeSource(t, dir, "calc.go", "package x\n\nfunc f() int {\n\treturn 1\n}\n")
	rule := ruleWithDigest(t, dir, extract.Ref{Path: "calc.go", Lines: "3-5"})

	// Truncate the file so the cited span no longer resolves -> broken.
	writeSource(t, dir, "calc.go", "package x\n")
	if r := Check([]graph.Node{rule}, dir); r.Broken != 1 {
		t.Fatalf("shrunk file should be broken, got %+v", r)
	}

	// A rule with no stored digest -> unknown.
	noDigest := graph.Node{ID: "rule.x.r2", Type: "rule", Sources: []extract.Ref{{Path: "calc.go", Lines: "1-1"}}}
	if r := Check([]graph.Node{noDigest}, dir); r.Unknown != 1 || r.Drifted() {
		t.Fatalf("rule without digest should be unknown, got %+v", r)
	}
}
