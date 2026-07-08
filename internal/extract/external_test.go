package extract

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// echoBin is the precompiled test extractor, built once in TestMain so the
// timeout test is not polluted by `go run` compilation time.
var echoBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "echoextractor")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	echoBin = filepath.Join(dir, "echoextractor")
	if out, buildErr := exec.Command("go", "build", "-o", echoBin, "./testdata/echoextractor").CombinedOutput(); buildErr != nil {
		fmt.Fprintf(os.Stderr, "build echoextractor: %v\n%s", buildErr, out)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func TestRunExtractorSuccess(t *testing.T) {
	facts, err := RunExtractor(context.Background(), ExtractorConfig{
		Name:    "echo",
		Cmd:     "go",
		Args:    []string{"run", "./testdata/echoextractor", "--mode", "ok", "--root", "{src}"},
		Timeout: 120 * time.Second,
	}, "/repo/src")
	if err != nil {
		t.Fatalf("RunExtractor: %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("facts = %d, want 1", len(facts))
	}
	f := facts[0]
	if f.Origin != "echo" {
		t.Errorf("origin = %q, want echo (forced to extractor name)", f.Origin)
	}
	if f.Attrs["root"] != "/repo/src" {
		t.Errorf("{src} not substituted, got %q", f.Attrs["root"])
	}
	if f.Certainty != CertaintyProved {
		t.Errorf("certainty = %q, want proved", f.Certainty)
	}
}

func TestRunExtractorTimeout(t *testing.T) {
	_, err := RunExtractor(context.Background(), ExtractorConfig{
		Name:    "echo",
		Cmd:     echoBin,
		Args:    []string{"--mode", "hang"},
		Timeout: 500 * time.Millisecond,
	}, "/repo/src")
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("want timeout error, got %v", err)
	}
}

func TestRunExtractorNonZeroExit(t *testing.T) {
	_, err := RunExtractor(context.Background(), ExtractorConfig{
		Name: "echo",
		Cmd:  echoBin,
		Args: []string{"--mode", "fail"},
	}, "/repo/src")
	if err == nil {
		t.Fatal("want error on non-zero exit")
	}
}

func TestRunExtractorInvalidJSON(t *testing.T) {
	_, err := RunExtractor(context.Background(), ExtractorConfig{
		Name: "echo",
		Cmd:  echoBin,
		Args: []string{"--mode", "garbage"},
	}, "/repo/src")
	if err == nil || !strings.Contains(err.Error(), "invalid facts JSON") {
		t.Fatalf("want invalid JSON error, got %v", err)
	}
}

func TestLoadFactsFile(t *testing.T) {
	facts, err := LoadFactsFile(filepath.Join("..", "..", "testdata", "fixture", "fixture.facts.json"))
	if err != nil {
		t.Fatalf("LoadFactsFile: %v", err)
	}
	if len(facts) != 3 {
		t.Fatalf("facts = %d, want 3", len(facts))
	}
	kinds := map[string]int{}
	for _, f := range facts {
		kinds[f.Kind]++
		if f.Origin == "" {
			t.Errorf("fact %s has empty origin", f.ID)
		}
	}
	if kinds["route"] != 2 || kinds["table"] != 1 {
		t.Fatalf("kinds = %v, want 2 routes and 1 table", kinds)
	}
}
