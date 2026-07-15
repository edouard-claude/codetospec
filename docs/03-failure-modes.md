# 3. Failure modes

This is the part nobody writes down, because you only learn it by shipping a
wrong graph and tracing back why. Each entry: **symptom → cause → fix.**

---

## 3.1 The line-number trap ★

**The most dangerous bug in code-to-spec, because it silently corrupts your
own quality metric.**

**Symptom.** Your adversarial reviewer rejects a large share of rules as
"unsupported." You inspect them by hand and find they are *true* rules — the
behavior is real — but their cited lines point a few lines off, and the offset
*grows with depth* into the file. Your confidence metric craters, and it's
lying to you.

**Cause.** You send the model a chunk with a header like `FILE: x (lines
659–1548)` and then the code **without line numbers**. To cite, the model has
to *count* lines from the top of the chunk — and it under-counts, drifting
further the deeper the line. The reviewer, which reads the *real* lines, then
correctly rejects a correctly-stated rule with a wrong citation. You are
measuring your prompt's formatting, not your rules' quality.

**Fix.** Prefix **every code line with its absolute line number** in the
prompt, and tell the model to *copy the number shown*, never count. This is a
one-line change with outsized impact — on a real PHP run it turned ~500
"unsupported" rules back into supported ones. It may also make symbol-grounding
(SCIP) unnecessary for citation accuracy: much of what looked like "imprecise
citations" was this counting drift.

**The second-order trap.** If you split a large class into per-method chunks
and prepend the class header for context, naive numbering (`start + i` over the
concatenation) shifts every body line by the header length — so body citations
land *past* the chunk end. Number the context block from its **own** real
start line, keep it non-citable, and number only the method body from the
chunk start. Every citable number must be true to the source.

**Lesson.** If the model has to *compute* a coordinate you could have *given*
it, give it. Counting is where models drift.

---

## 3.2 Hallucination, and why context windows don't fix it

**Symptom.** The model states a rule that sounds right but isn't in the code
(a plausible field name, an assumed threshold).

**Cause.** Asked to *produce* structure, a model fills gaps with priors. A
bigger context window doesn't help — it lets the model read more, not invent
less.

**Fix.** Never let the model produce facts. Feed it deterministically-extracted
symbols/definitions and require it to cite. A rule grounded in a resolvable
citation can be checked; an ungrounded assertion can't. (Injecting a symbol's
*exact definition* into the prompt also suppresses fabricated field names — the
model stops guessing what it can see.)

---

## 3.3 Reduce truncation on large domains

**Symptom.** A domain with hundreds of candidate rules fails consolidation with
"unexpected end of JSON input," and no `--max-tokens` value saves it — raise it
and the wall just moves.

**Cause.** You send all candidates in one call and expect all consolidated
rules back in one JSON response. Past a certain size, the output truncates.

**Fix.** Batch the reduce (a bounded number of candidates per call) and merge
deterministically. A batch that *still* truncates is split in half and
retried down to a floor — so growth in output can never lose a whole batch, let
alone a domain. Fail *fast* on truncation (don't waste correction rounds
re-sending at the same size; halving is the recovery).

**Watch for.** A domain with 900+ candidates is usually a smell — it's often
plumbing (e.g. framework resolvers) that should be excluded upstream, not
heroically consolidated.

---

## 3.4 The mega-domain

**Symptom.** 97% of your rules land in one domain called `core`. The graph is a
hairball; the per-domain reduce is really one giant blind batch.

**Cause.** Domain derivation takes one namespace segment, and the whole repo
lives under a single root (`App\Core\…`, `com.company.…`). One segment isn't
enough discrimination.

**Fix.** Make domain depth configurable (`--domain-depth 2|3`) — deriving
`core-controller`, `core-server`, `core-billing` from deeper segments gives
*semantic* domains, and the reduce consolidates coherent groups instead of one
undifferentiated mass. It's also a throughput fix: a 770-candidate `core`
domain reduces as ~26 sequential batches on one worker; splitting it lets the
worker pool run several real domains in parallel.

---

## 3.5 The metrics that lie

Two per-rule signals look useful and aren't:

- **Model-reported confidence is uninformative.** Ask a model how confident it
  is and it says ~0.9 for everything. On one real run, *every* rule scored
  ≥ 0.9. Don't trust self-reported confidence; derive a real signal from the
  adversarial verdict, citation-symbol overlap, and cross-candidate agreement.
- **Explicit/implicit origin degenerates.** Asked to label rules
  explicit-vs-implicit, models mark ~99% "explicit." Either the distinction is
  real and rare, or the prompt frames it poorly — either way it doesn't
  discriminate. The signals that *do* carry weight in practice: **nature**
  (business / presentation / technical) and the **adversarial verdict**.

**Lesson.** A field you added is not a signal until you've measured its
distribution. A signal with no variance is decoration.

---

## 3.6 Over-extraction (plumbing as "business rules")

**Symptom.** Hundreds of "rules" like *"the system shall return `$this` for
chaining"* or *"the system shall define a struct with these fields."*

**Cause.** On framework-heavy or SDK code, most lines are plumbing, and the map
dutifully describes them.

**Fix.** Classify every rule by **nature** (business / presentation /
technical) so the delivered spec is the *business* subset. The value to a
non-technical reviewer is not "all 700 rules" — it's the ~100 that are real
business behavior *and* citation-verified. Everything else stays available but
out of the way. Dead-code is a related smell: a controller never referenced by
any route fact can have its rules flagged as such.

---

## 3.7 Network stalls at scale

**Symptom.** A phase sits at 0% CPU for ~10 minutes on one domain.

**Cause.** A single LLM call is timing out and retrying (e.g. 180s × 3 with
backoff ≈ 9 min for one stuck call), often from provider rate-limiting on a
large request.

**Fix.** Parallelize independent work (map is naturally parallel; so is reduce
— domains are independent, cross-domain edges are computed later). Size worker
counts to the provider's limits, and prefer smaller requests. A timeout
proportional to request size and a fail-fast circuit help; near-term, smaller
chunks and batches reduce the blast radius.

---

## 3.8 Contradictory verdicts on the same citation

**Symptom.** Several rules cite the *same* span and get contradictory verdicts.

**Cause.** Usually a symptom of 3.1 — when the model can't cite precisely, many
rules cite the same giant blob, and the reviewer judges a 900-line blob
inconsistently. Fix the citations (3.1) and rules cite distinct spans; the
inconsistency evaporates.

**Do not** cache verdicts by cited-code digest to "resolve" it: two *different*
rules citing the same code should be allowed *different* verdicts. That would
be semantically wrong.

---

→ Next: **[State of the art](04-state-of-the-art.md)**
