// Package config resolves the CLI configuration from flags, the optional
// YAML config file and environment variables, in that precedence order.
package config

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Extractor configures one external facts extractor command.
type Extractor struct {
	Name    string   `yaml:"name"`
	Cmd     string   `yaml:"cmd"`
	Args    []string `yaml:"args"` // "{src}" is substituted with --src
	Timeout string   `yaml:"timeout"`
}

// TimeoutDuration parses the configured timeout, defaulting to 300s.
func (e Extractor) TimeoutDuration() (time.Duration, error) {
	if e.Timeout == "" {
		return 300 * time.Second, nil
	}
	d, err := time.ParseDuration(e.Timeout)
	if err != nil {
		return 0, fmt.Errorf("extractor %s: invalid timeout %q: %w", e.Name, e.Timeout, err)
	}
	return d, nil
}

// fileConfig models codetospec.yaml; every field is optional.
type fileConfig struct {
	Exclude        []string    `yaml:"exclude"`
	Extractors     []Extractor `yaml:"extractors"`
	FactsFiles     []string    `yaml:"facts_files"`
	DomainStrategy string      `yaml:"domain_strategy"`
}

// Config is the resolved configuration for one command invocation.
type Config struct {
	Src            string
	Out            string
	BaseURL        string
	APIKey         string
	Model          string
	Lang           string
	Workers        int
	MaxTokens      int
	Exclude        []string
	FactsFiles     []string
	LogLevel       string
	Extractors     []Extractor
	DomainStrategy string
	NoTUI          bool
	Crosscheck     bool
	Repair         bool
	ReduceBatch    int
}

// DefaultExclude lists the directories ignored by default.
var DefaultExclude = []string{"vendor", "node_modules", "storage", "dist", "build", ".git"}

// stringList is a repeatable string flag.
type stringList []string

func (l *stringList) String() string { return strings.Join(*l, ",") }

func (l *stringList) Set(v string) error {
	*l = append(*l, v)
	return nil
}

// ParseRun resolves the run command configuration.
func ParseRun(args []string) (*Config, error) {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	cfg := &Config{}
	var (
		configPath string
		excludeCSV string
		facts      stringList
	)
	fs.StringVar(&cfg.Src, "src", "", "root of the analyzed repository (required)")
	fs.StringVar(&cfg.Out, "out", "", "output graph directory (required)")
	fs.StringVar(&configPath, "config", "", "config file (default <src>/codetospec.yaml when present)")
	fs.StringVar(&cfg.BaseURL, "base-url", os.Getenv("LLM_BASE_URL"), "OpenAI-compatible endpoint")
	fs.StringVar(&cfg.APIKey, "api-key", os.Getenv("LLM_API_KEY"), "API key (empty accepted for local vLLM)")
	fs.StringVar(&cfg.Model, "model", os.Getenv("LLM_MODEL"), "model name")
	fs.StringVar(&cfg.Lang, "lang", "fr", "requirements language (fr or en)")
	fs.IntVar(&cfg.Workers, "workers", 4, "map parallelism")
	fs.IntVar(&cfg.MaxTokens, "max-tokens", 4096, "generation cap per LLM call")
	fs.StringVar(&excludeCSV, "exclude", strings.Join(DefaultExclude, ","), "comma-separated ignored directories")
	fs.Var(&facts, "facts", "additional facts JSON file (repeatable)")
	fs.StringVar(&cfg.LogLevel, "log-level", "info", "debug, info, warn or error")
	fs.BoolVar(&cfg.NoTUI, "no-tui", false, "disable the full-screen dashboard, log to stderr")
	fs.BoolVar(&cfg.Crosscheck, "crosscheck", false, "adversarial review pass: a fresh-context LLM tries to refute each rule against its cited lines")
	fs.BoolVar(&cfg.Repair, "repair", false, "repair pass (implies --crosscheck): a flagged rule re-cites the exact span of a precise symbol; needs a SCIP index")
	fs.IntVar(&cfg.ReduceBatch, "reduce-batch", 30, "max candidate rules per reduce call; larger domains are batched then merged")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	if cfg.Src == "" || cfg.Out == "" {
		return nil, errors.New("run requires --src and --out")
	}
	if cfg.Repair {
		cfg.Crosscheck = true
	}
	explicit := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { explicit[f.Name] = true })

	fileCfg, err := loadFile(configPath, cfg.Src)
	if err != nil {
		return nil, err
	}

	cfg.Exclude = splitCSV(excludeCSV)
	if !explicit["exclude"] && len(fileCfg.Exclude) > 0 {
		cfg.Exclude = fileCfg.Exclude
	}
	cfg.FactsFiles = append([]string(nil), facts...)
	for _, f := range fileCfg.FactsFiles {
		if !slices.Contains(cfg.FactsFiles, f) {
			cfg.FactsFiles = append(cfg.FactsFiles, f)
		}
	}
	cfg.Extractors = fileCfg.Extractors
	cfg.DomainStrategy = fileCfg.DomainStrategy
	if cfg.DomainStrategy == "" {
		cfg.DomainStrategy = "auto"
	}

	if err := cfg.validateRun(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// ParseVerify resolves the verify command configuration.
func ParseVerify(args []string) (*Config, error) {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	cfg := &Config{}
	fs.StringVar(&cfg.Src, "src", "", "root of the analyzed repository (required)")
	fs.StringVar(&cfg.Out, "out", "", "graph directory to verify (required)")
	fs.StringVar(&cfg.LogLevel, "log-level", "info", "debug, info, warn or error")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if cfg.Src == "" || cfg.Out == "" {
		return nil, errors.New("verify requires --src and --out")
	}
	return cfg, nil
}

// ParseStats resolves the stats command configuration.
func ParseStats(args []string) (*Config, error) {
	fs := flag.NewFlagSet("stats", flag.ContinueOnError)
	cfg := &Config{}
	fs.StringVar(&cfg.Out, "out", "", "graph directory (required)")
	fs.StringVar(&cfg.LogLevel, "log-level", "info", "debug, info, warn or error")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if cfg.Out == "" {
		return nil, errors.New("stats requires --out")
	}
	return cfg, nil
}

func (c *Config) validateRun() error {
	if info, err := os.Stat(c.Src); err != nil || !info.IsDir() {
		return fmt.Errorf("--src %q is not a readable directory", c.Src)
	}
	if c.Lang != "fr" && c.Lang != "en" {
		return fmt.Errorf("--lang %q must be fr or en", c.Lang)
	}
	if c.Workers < 1 {
		return fmt.Errorf("--workers %d must be >= 1", c.Workers)
	}
	if c.MaxTokens < 1 {
		return fmt.Errorf("--max-tokens %d must be >= 1", c.MaxTokens)
	}
	switch c.DomainStrategy {
	case "auto", "namespace", "directory":
	default:
		return fmt.Errorf("domain_strategy %q must be auto, namespace or directory", c.DomainStrategy)
	}
	if c.BaseURL == "" {
		return errors.New("missing LLM endpoint: set --base-url or LLM_BASE_URL")
	}
	if c.Model == "" {
		return errors.New("missing LLM model: set --model or LLM_MODEL")
	}
	for _, e := range c.Extractors {
		if e.Name == "" || e.Cmd == "" {
			return fmt.Errorf("extractor entries need both name and cmd (got name=%q cmd=%q)", e.Name, e.Cmd)
		}
		if _, err := e.TimeoutDuration(); err != nil {
			return err
		}
	}
	return nil
}

// loadFile reads the YAML config file: the explicit --config path, or
// <src>/codetospec.yaml when present.
func loadFile(configPath, src string) (fileConfig, error) {
	var fc fileConfig
	path := configPath
	if path == "" {
		candidate := filepath.Join(src, "codetospec.yaml")
		if _, err := os.Stat(candidate); err == nil {
			path = candidate
		} else if !errors.Is(err, fs.ErrNotExist) {
			return fc, fmt.Errorf("stat %s: %w", candidate, err)
		}
	}
	if path == "" {
		return fc, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fc, fmt.Errorf("read config %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, &fc); err != nil {
		return fc, fmt.Errorf("parse config %s: %w", path, err)
	}
	return fc, nil
}

func splitCSV(s string) []string {
	var out []string
	for part := range strings.SplitSeq(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
