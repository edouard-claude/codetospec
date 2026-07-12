# codetospec

Turn a legacy codebase into a versionable, verifiable knowledge graph of its
business rules.

```text
 codetospec  old-legacy-app → ./spec-graph                          deepseek-chat · 8 workers

 ✓ extract   118 fichiers · 512 facts (import 61 · module 14 · route 8 · symbol 402 · table 27)
          extracteur go: ok · 35 facts
          extracteur scip: ok · 402 facts
 ✓ chunk     460 chunks
 ✓ map       ███████████████████████████████████████████████  100% 460/460 · 920 règles candidates
 ✓ reduce    21/21 domaines · 252 règles finales
 ● check     210/312 règles contre-vérifiées · 98 supported · 48 réparés · 32 partial · 32 à revoir

╭──────────────────────────────────────────────────────────────────────────────────────────────╮
│ repaired rule.billing.prorata-refund → cited span 88-140 overlaps func Refund                │
╰──────────────────────────────────────────────────────────────────────────────────────────────╯

 tokens map 1.1M+322k · reduce 189k+88k · total 1.6M+450k   6m12s   [q] quitter
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
nature: business          # business | presentation | technical
origin: explicit          # explicit | implicit
confidence: 0.90
crosscheck: supported     # supported | partial | unsupported
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
- **An adversarial reviewer refutes each rule** (`--crosscheck`): a
  fresh-context LLM sees only the rule and the lines it cites, and votes
  `supported | partial | unsupported`. A disputed rule is flagged, never
  silently dropped.
- **Flagged rules are repaired, not just flagged** (`--repair`, needs a SCIP
  index): a rule the reviewer rejected gets one chance to re-cite the exact
  span of a precise symbol that implements it. The new citation is accepted
  only if it mechanically overlaps a real symbol body — grounded by
  construction.
- **Rules are classified**, not just listed: `nature` (business /
  presentation / technical) and `origin` (explicit / implicit) separate real
  business rules from plumbing; `confidence` × `crosscheck` gives a triage
  signal.

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
   ecosystem's own tooling. Three ship today — PHP/Laravel (routes, schema
   tables, optional runtime introspection), Go (gin/echo/chi routes and SQL
   tables via `go/packages`), and a SCIP converter that turns any indexer's
   output into precise symbol facts. Adding an ecosystem means writing an
   executable, zero binary changes.
3. **The LLM**: reads chunks directly, natively multi-language.

Any OpenAI-compatible endpoint works: a self-hosted vLLM, DeepSeek, or any
other `/chat/completions` provider — pick your model with three environment
variables.

## Pipeline

```
extract (layers 1+2) ──▶ chunk (AST) ──▶ map (LLM, parallel, per chunk)
       ──▶ reduce (LLM, per domain) ──▶ crosscheck (LLM, optional)
       ──▶ build + verify + render (pure Go)
```

- **map**: one call per chunk extracts candidate rules with citations. When
  a SCIP index is supplied (via the SCIP converter), each chunk's prompt is
  grounded with the exact signatures and line spans of the symbols it
  contains, so the model cites real code spans instead of guessing.
- **reduce**: one call per domain deduplicates and consolidates candidates;
  citations must exist verbatim among the candidates — the model cannot
  invent or alter them. A domain with more than `--reduce-batch` candidates
  (default 30) is reduced in batches and merged; a batch that still truncates
  is split in half and retried, so a large domain never loses its rules.
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
bin/codetospec drift  --src <repo> --out <graph>   # flag rules whose cited code changed, exit 1 on drift
bin/codetospec stats  --out <graph>                # phase counters and token costs
```

Each rule stores a digest of its cited code, so `drift` reports which rules
went stale as the source evolves — the spec stays honest about what it still
matches. The output README also lists near-duplicate rule candidates (a
deterministic consistency check) for a reviewer to reconcile.

Useful flags: `--crosscheck` (adversarial review pass), `--repair` (fix
flagged citations, needs a SCIP index), `--workers N` (map
parallelism), `--max-tokens N` (raise it if a large domain truncates its
reduce output), `--lang fr|en` (requirements language),
`--exclude 'vendor,node_modules,*.md,*.csv'` (directory names and
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
