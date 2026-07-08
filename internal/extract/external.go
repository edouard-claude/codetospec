package extract

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// FactsSchema is the JSON contract identifier expected on an extractor's
// stdout and in facts files.
const FactsSchema = "codetospec/facts/v1"

// ExtractorConfig describes one external extractor command.
type ExtractorConfig struct {
	Name    string
	Cmd     string
	Args    []string // "{src}" is substituted with the analyzed root
	Timeout time.Duration
}

// FactsEnvelope is the JSON payload of the extractor protocol.
type FactsEnvelope struct {
	Schema string `json:"schema"`
	Facts  []Fact `json:"facts"`
}

// RunExtractor executes one external extractor and parses its stdout.
// The command runs from the current working directory with the configured
// timeout; stderr is forwarded to debug logs. Any failure (non-zero exit,
// invalid JSON, wrong schema) is returned as an error so the caller can
// degrade gracefully.
func RunExtractor(ctx context.Context, cfg ExtractorConfig, src string) ([]Fact, error) {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 300 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := make([]string, len(cfg.Args))
	for i, a := range cfg.Args {
		args[i] = strings.ReplaceAll(a, "{src}", src)
	}

	cmd := exec.CommandContext(runCtx, cfg.Cmd, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if stderr.Len() > 0 {
		slog.Debug("extractor stderr", "name", cfg.Name, "output", strings.TrimSpace(stderr.String()))
	}
	if runCtx.Err() == context.DeadlineExceeded {
		return nil, fmt.Errorf("extractor %s timed out after %s", cfg.Name, timeout)
	}
	if err != nil {
		return nil, fmt.Errorf("extractor %s: %w", cfg.Name, err)
	}

	facts, err := decodeEnvelope(stdout.Bytes())
	if err != nil {
		return nil, fmt.Errorf("extractor %s: %w", cfg.Name, err)
	}
	for i := range facts {
		facts[i].Origin = cfg.Name
	}
	return facts, nil
}

// LoadFactsFile reads one facts JSON file (same schema as the extractor
// protocol). Facts keep their origin when set, defaulting to the file name.
func LoadFactsFile(path string) ([]Fact, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read facts file: %w", err)
	}
	facts, err := decodeEnvelope(data)
	if err != nil {
		return nil, fmt.Errorf("facts file %s: %w", path, err)
	}
	origin := filepath.Base(path)
	for i := range facts {
		if facts[i].Origin == "" {
			facts[i].Origin = origin
		}
	}
	return facts, nil
}

func decodeEnvelope(data []byte) ([]Fact, error) {
	var envelope FactsEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("invalid facts JSON: %w", err)
	}
	if envelope.Schema != FactsSchema {
		return nil, fmt.Errorf("unexpected facts schema %q, want %q", envelope.Schema, FactsSchema)
	}
	return envelope.Facts, nil
}
