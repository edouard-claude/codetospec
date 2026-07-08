# codetospec

Turn a legacy codebase into a versionable, verifiable knowledge graph of its
business rules.

```text
 codetospec  old-legacy-app → ./spec-graph                      deepseek-chat · 4 workers

 ✓ extract   24 fichiers · 69 facts (import 14 · module 6 · route 5 · symbol 41 · table 3)
          extracteur php: ok · 57 facts
 ● chunk     38 chunks
 ● map       ███████████████████████████████░░░░░░░░░░░░░  68% 26/38 · 53 règles candidates
          ⣾ app/legacy/report.php:1-250
 ○ reduce    en attente

╭────────────────────────────────────────────────────────────────────────────────────────╮
│ parse failed, falling back to line chunks path=assets/style.css                        │
│ chunk failed chunk=9f31c02a path=app/divers2.php err=rejected after 2 corrections      │
╰────────────────────────────────────────────────────────────────────────────────────────╯

 tokens map 112k+16k · reduce 0+0 · total 112k+16k   4m32s   [q] quitter
```

`codetospec` reads a source repository and produces a markdown graph: domain,
entity, endpoint and rule nodes (requirements written in the EARS format),
linked by typed edges in YAML frontmatter. The output is meant to be read by
humans (GitLab, Obsidian) **and** consumed by agents (`graph.json`,
`llms.txt`) — typically as the specification baseline for a rewrite.

```
---
id: rule.billing.prorata-activation
type: rule
status: generated
sources:
  - path: app/Services/Billing/ProrataCalculator.php
    lines: "42-118"
edges:
  - {type: belongs_to, to: domain.billing}
  - {type: touches, to: entity.invoices}
ears: event
acceptance: 3
---

# Prorated billing on activation

**Requirement (EARS)**: WHEN a subscriber activates mid-month, the system
shall bill prorated to the remaining days.
...
```

## The approach

LLMs are good at reading code and bad at being trusted. `codetospec` keeps
the LLM inside a loop it does not control:

- **The program drives; the LLM only does the cognitive work.** Chunking,
  merging, graph building and cross-domain edges are computed
  deterministically. The LLM never invents structure.
- **Every LLM output is mechanically validated** before it enters the graph:
  JSON schema, EARS pattern, citations that must resolve to real files and
  line ranges, references that must exist. Invalid output is sent back for
  correction (twice max), then the unit is marked failed and the run
  continues — one bad chunk never kills a run.
- **Mechanically extracted facts are the anchors.** Symbols, routes, tables
  and imports are extracted by parsers, not by the model. The LLM consumes
  facts; it never produces them.
- **Everything is a citation.** Each rule points to exact `path:lines` in the
  source. `codetospec verify` re-checks a graph against the tree at any time.

## Language-agnostic by construction

The core knows nothing about any particular language. Language knowledge
lives in three layers:

1. **tree-sitter** (universal structure): error-tolerant AST for chunking and
   symbols, whatever state the code is in. Grammars are driven by declarative
   `.scm` query files — adding a language means adding a grammar and one
   query file, zero core changes. PHP, Go, JavaScript, TypeScript/TSX and
   Rust ship out of the box; unknown files fall back to line-window chunks.
2. **External native extractors** (ecosystem semantics): standalone
   executables speaking a small JSON protocol, free to use the target
   ecosystem's own tooling. A PHP extractor ships as the first example
   (routes, schema tables, optional runtime introspection). Adding an
   ecosystem means writing an executable, zero binary changes.
3. **The LLM**: reads chunks directly, natively multi-language.

Any OpenAI-compatible endpoint works: a self-hosted vLLM, DeepSeek, or any
other `/chat/completions` provider — pick your model with three environment
variables.

## Pipeline

```
extract (layers 1+2) ──▶ chunk (AST) ──▶ map (LLM, parallel, per chunk)
       ──▶ reduce (LLM, per domain) ──▶ build + verify + render (pure Go)
```

- **map**: one call per chunk extracts candidate rules with citations.
- **reduce**: one call per domain deduplicates and consolidates candidates;
  citations must exist verbatim among the candidates — the model cannot
  invent or alter them.
- **build/verify/render**: nodes are assembled and cross-checked (unique
  ids, resolvable edges and citations, frontmatter round-trip). Nothing is
  written unless verification passes.

Runs are **resumable** (Ctrl-C safe: state is saved after every unit, cached
units are skipped on rerun) and **deterministic** (two warm-cache runs
produce byte-for-byte identical output). Token usage is tracked per phase
and shown live.

## Quick start

Requirements: Go ≥ 1.26, a C toolchain (CGo for tree-sitter), an
OpenAI-compatible endpoint.

```sh
make build

export LLM_BASE_URL=https://api.deepseek.com/v1   # or your vLLM, or any provider
export LLM_API_KEY=sk-...                          # empty accepted (local vLLM)
export LLM_MODEL=deepseek-chat

bin/codetospec run --src /path/to/legacy-repo --out /path/to/spec-graph
```

On an interactive terminal, `run` shows a full-screen dashboard (files
parsed, facts, map/reduce progress, failures, live token counts; `q` stops
cleanly and the run resumes later). Use `--no-tui` for plain logs (CI).

Other commands:

```sh
bin/codetospec verify --src <repo> --out <graph>   # re-check citations, exit 1 on violation
bin/codetospec stats  --out <graph>                # phase counters and token costs
```

Useful flags: `--workers N` (map parallelism), `--max-tokens N` (raise it if
a large domain truncates its reduce output), `--lang fr|en` (requirements
language), `--exclude 'vendor,node_modules,*.md,*.csv'` (directory names and
file globs), `--facts extra.facts.json` (inject facts from anywhere).

## Output

```
<out>/
├── llms.txt          # navigation guide for agents
├── README.md         # summary, coverage report, Mermaid domain graph
├── graph.json        # whole graph, machine format
└── nodes/
    ├── domains/<slug>.md
    ├── entities/<slug>.md
    ├── endpoints/<slug>.md
    └── rules/<domain>.<slug>.md
```

Domain `depends_on` edges are computed from import facts crossing domain
boundaries — never by the LLM. The output README reports coverage: endpoints
referenced by at least one rule, entities touched, failed chunks/domains,
files without a grammar.

## Extractor protocol (facts v1)

An extractor is any executable configured in `codetospec.yaml` that writes
this to stdout:

```json
{"schema": "codetospec/facts/v1", "facts": [
  {"kind": "route", "id": "route.post./api/activate",
   "attrs": {"method": "POST", "path": "/api/activate", "controller": "..."},
   "source": {"path": "routes/web.php", "lines": "5-5"},
   "origin": "php", "certainty": "static"}
]}
```

```yaml
# codetospec.yaml (at the analyzed repo root)
extractors:
  - name: php
    cmd: php
    args: [extractors/php/extract.php, --root, "{src}"]
    timeout: 300s
```

`kind`: `symbol`, `route`, `table`, `module`, `import`. `certainty`:
`static` or `proved` (runtime-verified). Facts merge by id with priority
`proved` > extractor > tree-sitter, so a runtime-proved route overrides a
statically guessed one. A failing extractor is a warning, not a fatal error.

## Limitations (v0.1)

- Citations are guaranteed to resolve inside the cited chunk and file — not
  to point at the exact statement.
- Deduplication happens per domain; the same behavior surfaced in two
  domains yields two rules.
- No agentic reduce, no SQLite, no MCP server, no CI drift-check yet — see
  the roadmap in the spec.

## License

MIT
