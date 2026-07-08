package sitter

import (
	"os"
	"path/filepath"
	"testing"
)

func readFixture(t *testing.T, rel string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "fixture", filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read fixture %s: %v", rel, err)
	}
	return data
}

func TestParsePHPFixtureDefinitions(t *testing.T) {
	content := readFixture(t, "app/Services/Billing/ProrataCalculator.php")
	info, err := Parse("php", content)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	var class, method *Definition
	for i := range info.Definitions {
		d := &info.Definitions[i]
		switch {
		case d.Kind == "class" && d.Name == "ProrataCalculator":
			class = d
		case d.Kind == "method" && d.Name == "calculate":
			method = d
		}
	}
	if class == nil {
		t.Fatalf("class ProrataCalculator not found in %+v", info.Definitions)
	}
	if class.StartLine != 7 || class.EndLine != 25 {
		t.Errorf("class lines = %d-%d, want 7-25", class.StartLine, class.EndLine)
	}
	if method == nil {
		t.Fatalf("method calculate not found in %+v", info.Definitions)
	}
	if method.StartLine != 11 || method.EndLine != 24 {
		t.Errorf("method lines = %d-%d, want 11-24", method.StartLine, method.EndLine)
	}
	if method.Visibility != "public" {
		t.Errorf("method visibility = %q, want public", method.Visibility)
	}
}

func TestParsePHPFixtureNamespaceAndImports(t *testing.T) {
	content := readFixture(t, "app/Http/Controllers/ActivationController.php")
	info, err := Parse("php", content)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(info.Modules) != 1 || info.Modules[0].Name != `App\Http\Controllers` {
		t.Fatalf("modules = %+v, want App\\Http\\Controllers", info.Modules)
	}
	targets := make(map[string]bool)
	for _, imp := range info.Imports {
		targets[imp.Target] = true
	}
	for _, want := range []string{`App\Models\Invoice`, `App\Services\Billing\ProrataCalculator`} {
		if !targets[want] {
			t.Errorf("import %q not found in %+v", want, info.Imports)
		}
	}
}

func TestLanguageForPath(t *testing.T) {
	cases := map[string]string{
		"a/b.php":  "php",
		"a/b.go":   "go",
		"a/b.ts":   "typescript",
		"a/b.tsx":  "tsx",
		"a/b.rs":   "rust",
		"a/b.js":   "javascript",
		"a/b.xyz":  "",
		"Makefile": "",
	}
	for path, want := range cases {
		if got := LanguageForPath(path); got != want {
			t.Errorf("LanguageForPath(%q) = %q, want %q", path, got, want)
		}
	}
}

// TestAllGrammarQueriesCompile parses a snippet in every registered language
// so a broken .scm file fails fast.
func TestAllGrammarQueriesCompile(t *testing.T) {
	snippets := map[string]string{
		"php":        "<?php\nnamespace A\\B;\nuse C\\D;\nclass X { public function m() {} }\nfunction f() {}\n",
		"go":         "package main\n\nimport \"fmt\"\n\ntype T struct{}\n\nfunc main() { fmt.Println(1) }\n",
		"javascript": "import x from 'y';\nclass A { m() { return 1 } }\nfunction f() {}\n",
		"typescript": "import x from 'y';\ninterface I { a: string }\ntype T = string;\nenum E { A }\nclass C { m(): void {} }\n",
		"tsx":        "import x from 'y';\nclass C { m(): void {} }\nfunction f() { return 1 }\n",
		"rust":       "use std::fmt;\nmod m {}\nstruct S;\nenum E { A }\ntrait T {}\nimpl S {}\nfn f() {}\n",
	}
	for language, snippet := range snippets {
		info, err := Parse(language, []byte(snippet))
		if err != nil {
			t.Errorf("Parse(%s): %v", language, err)
			continue
		}
		if len(info.Definitions) == 0 {
			t.Errorf("Parse(%s): no definitions found", language)
		}
		if len(info.Imports) == 0 {
			t.Errorf("Parse(%s): no imports found", language)
		}
	}
}

func TestParseUnknownLanguage(t *testing.T) {
	if _, err := Parse("cobol", []byte("x")); err == nil {
		t.Fatal("Parse(cobol) should fail")
	}
}
