//go:build phplocal

package extract

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestPHPExtractorOnFixture exercises the shipped native PHP extractor when
// php and composer are available locally. Run with: go test -tags phplocal.
func TestPHPExtractorOnFixture(t *testing.T) {
	if _, err := exec.LookPath("php"); err != nil {
		t.Skip("php not installed")
	}
	extractorDir, err := filepath.Abs(filepath.Join("..", "..", "extractors", "php"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(extractorDir, "vendor", "autoload.php")); err != nil {
		composer, lookErr := exec.LookPath("composer")
		if lookErr != nil {
			t.Skip("composer not installed and vendor/ absent")
		}
		cmd := exec.Command(composer, "install", "--no-interaction", "--quiet")
		cmd.Dir = extractorDir
		if out, installErr := cmd.CombinedOutput(); installErr != nil {
			t.Fatalf("composer install: %v\n%s", installErr, out)
		}
	}

	src, err := filepath.Abs(filepath.Join("..", "..", "testdata", "fixture"))
	if err != nil {
		t.Fatal(err)
	}
	facts, err := RunExtractor(context.Background(), ExtractorConfig{
		Name:    "php",
		Cmd:     "php",
		Args:    []string{filepath.Join(extractorDir, "extract.php"), "--root", "{src}"},
		Timeout: 120 * time.Second,
	}, src)
	if err != nil {
		t.Fatalf("RunExtractor: %v", err)
	}

	kinds := map[string]int{}
	ids := map[string]bool{}
	for _, f := range facts {
		kinds[f.Kind]++
		ids[f.ID] = true
		if f.Origin != "php" {
			t.Errorf("fact %s origin = %q, want php", f.ID, f.Origin)
		}
	}
	if kinds["route"] < 2 {
		t.Errorf("routes = %d, want >= 2", kinds["route"])
	}
	if !ids["table.invoices"] {
		t.Error("table.invoices fact missing")
	}
	if !ids["route.post./api/activate"] {
		t.Error("route.post./api/activate fact missing")
	}
	if kinds["symbol"] < 4 {
		t.Errorf("symbols = %d, want >= 4 (classes + public methods)", kinds["symbol"])
	}
}
