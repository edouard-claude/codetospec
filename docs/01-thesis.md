# 1. The thesis: verification is the bottleneck

The reflexive 2026 move is: *give the code to an LLM and ask for the rewrite,
or the spec.* It fails — and not for the reason people assume.

## It's not about reading

The instinct is that the model can't fit or "understand" a large codebase, so
the fix is a bigger context window. That's the wrong diagnosis. Modern models
read code well. The problem is that **a plausible summary is not a verifiable
one**, and a one-million-token synthesis is exactly as unverifiable as a
one-thousand-token one — just longer.

On a system in production, the distance between *plausible* and *verifiable* is
a threshold that reads `> 300 seconds` in the code but `> 3600` in the
rewrite, shipped because nobody could confirm the original. Plausible is not
signable.

So the bottleneck is not the model's capacity to read. It is **your capacity
to trust what it wrote** — and trust does not come from a longer answer. It
comes from evidence you can check mechanically.

## The measured evidence

Two well-known rewrites bracket the space:

- **Jarred Sumner** rewrote Bun — 535,000 lines of Zig — into Rust with an
  agent. It worked, but only inside heavy scaffolding: specs first,
  adversarial reviewers in separate contexts, 60,000 untouchable tests. And
  19 regressions still shipped.
- **Terence Tao** ported decades-old Java applets to JavaScript in hours with
  no spec at all, near bug-free — because, in his words, the downside of a bug
  is low. They're visual aids.

Both are right. The variable between them is **the cost of a silent bug**. A
legacy business monolith sits in the worst quadrant: a bug costs real money,
and you have neither Sumner's tests nor his spec. That is precisely where you
cannot afford *plausible*, and precisely where most modernization work lives.

## The move: code → spec → code

Don't convert code → code. Insert a step that can be checked:

```
code  ──▶  verifiable specification  ──▶  code
```

The specification is the contract between the old system and the new. Its
acceptance criteria become the tests of the rewrite. The rewrite *implements
the spec* rather than *translating files* — which, as Bun showed, is what
injects invisible regressions.

The rest of this guide is about the one hard part: making that middle
artifact — the spec — something you can actually trust. That is an
engineering problem, not a prompting one.

→ Next: **[Architecture](02-architecture.md)**
