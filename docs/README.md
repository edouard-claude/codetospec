# Field Guide to Code-to-Spec

Turning a legacy codebase into a trustworthy specification of its business
rules — with an LLM you don't trust on its word.

This is not documentation for the `codetospec` tool (see the [project
README](../README.md) for that). It is a **practitioner's field guide to the
discipline**: the principles that make extracted specs trustworthy, the
failure modes nobody warns you about, and how the state of the art fits
together. It is written from the scars of building a working tool — the
lessons you only learn once the graph is wrong and you have to figure out why.

If you are extracting requirements, business rules, or a specification from
an old system with an LLM — in any language, with any framework — this is for
you.

## Contents

1. **[The thesis](01-thesis.md)** — why the bottleneck is verification, not
   reading, and why a bigger context window doesn't help.
2. **[Architecture](02-architecture.md)** — deterministic facts as anchors,
   the LLM for interpretation, mechanical verifiers around every step.
3. **[Failure modes](03-failure-modes.md)** — the hard-won catalog: the
   line-number trap, imprecise citations, reduce truncation, the mega-domain,
   and the metrics that lie. **Start here if you already have a pipeline.**
4. **[State of the art](04-state-of-the-art.md)** — Bun, Tao, Kiro,
   AgentModernize, and the risk-cost axis that tells you where each approach
   applies.
5. **[Honest limits](05-limits.md)** — what static code-to-spec cannot do, and
   why saying so is part of the product.

## The one-sentence version

The program holds the loop and sets deterministic gates; the LLM only fills
the boxes — always between two verifiers — and every rule it produces cites
the exact source lines that prove it, or is flagged as unproven.
