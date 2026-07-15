# 2. Architecture: anchors, interpretation, verifiers

The trustworthy-spec problem has one shape that recurs across the research and
across every working tool: **the program holds the loop; the LLM only does the
cognitive work, always between two deterministic gates.** Three layers.

## Layer 1 — Deterministic facts as anchors

Extract everything that can be *proven* without a model: symbols, functions,
classes, routes, tables, imports, exact signatures and line spans. Two
mechanisms cover most languages:

- **AST (tree-sitter)** — error-tolerant parsing for chunking and symbols,
  whatever state the code is in. Language knowledge lives in declarative query
  files, so adding a language is additive, not a core change.
- **Native extractors / code-intelligence indexes (SCIP, LSIF)** — ecosystem
  tools that resolve routes, schemas, and precise definitions the AST can't.

These facts are the **anchors**. The rule of the whole discipline: *the LLM
consumes facts; it never produces them.* A model that invents a fact has
nothing to be checked against. A model that only interprets facts can always
be checked against them.

## Layer 2 — The LLM for interpretation

The model does exactly one thing the deterministic layer can't: read chunks of
code and state the **business rules** in them. Two disciplines make its output
usable:

- **A constrained grammar.** Emit requirements in a fixed form — EARS (Easy
  Approach to Requirements Syntax: `WHEN <trigger>, the system shall
  <response>`, plus four other patterns). A constrained grammar is one you can
  validate.
- **Mandatory citations.** Every rule must cite exact source lines. A rule
  without a resolvable citation is not admitted. This is the single most
  important design choice: it turns "the model claims X" into "the model
  claims X, and here are the lines that must prove it."

## Layer 3 — Mechanical verifiers around every step

No LLM output enters the graph unchecked:

- **Schema + grammar validation** on each response; on failure, the exact
  error is fed back for a bounded number of corrections, then the unit is
  marked failed and the run continues. One bad chunk never kills a run.
- **Resolvable citations** — every `path:lines` must exist in the tree and fit
  the real file. Structural coherence, guaranteed.
- **Adversarial refutation** — a *second* model, in a fresh context, sees only
  the rule and the lines it cites and tries to *refute* it. The model that
  wrote a claim never certifies it. This is the semantic gate.
- **Repair, not just reject** — a rule the reviewer rejects gets one chance to
  re-cite the exact span of a precise symbol; the new citation is accepted
  only if it mechanically overlaps a real symbol body. Grounded by
  construction.

## The properties that make it operational

- **Determinism** — stable sorts everywhere; two warm-cache runs produce
  byte-identical output. Non-determinism anywhere is a bug.
- **Resumability** — content-hashed caches per unit; an interrupt loses
  nothing, and only changed code is re-processed.
- **Drift detection** — each rule stores a digest of its cited code; when the
  source evolves, the spec reports which rules went stale. The spec stays
  honest over time, which is what lets it live in CI.

## The pipeline

```
extract  ──▶ chunk ──▶ map ──▶ reduce ──▶ crosscheck(+repair)
   [deterministic]      [LLM, gated]          [LLM, gated]
  ──▶ build ──▶ digest ──▶ verify ──▶ render
        [all deterministic; nothing is written unless verify passes]
```

Every LLM box is bracketed by deterministic ones. That bracket *is* the
product.

→ Next: **[Failure modes](03-failure-modes.md)** — where this goes wrong in
practice.
