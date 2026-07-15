# 4. State of the art

Code-to-spec and AI-assisted modernization converged fast in 2024–2026. The
useful way to read the landscape is not a list of tools but a single axis: **the
cost of a silent bug.** Where a bug is cheap, precision and specs barely
matter; where it's expensive, they are the whole game.

## The two poles

- **Terence Tao** (2026) — ported decades-old Java applets to JavaScript in
  hours with an agent, no spec, near bug-free. His own explanation: these are
  visual aids, the downside risk is low, "the precision of the specification
  matters far less now." *Correct — for low-risk code.*
- **Jarred Sumner / Bun** (2026) — rewrote 535k lines of Zig to Rust with an
  agent, but wrapped in specs, adversarial reviewers in separate contexts, and
  60k untouchable tests. 19 regressions still shipped. *This is what
  high-stakes rewriting actually costs.*

Everything else lives between them, and a legacy business monolith is the worst
case: expensive bugs, none of Sumner's scaffolding.

## Products and research, mapped to the discipline

- **Amazon Kiro** — spec-driven development that formalizes requirements in
  EARS, then checks them with a reasoning engine (LLM interprets, a solver
  verifies). Confirms two of our pillars: EARS as the grammar, and a
  deterministic verifier behind the model.
- **GitHub Spec Kit** — makes the spec the authoritative artifact that drives
  implementation. The "spec as source of truth" direction, at scale.
- **AgentModernize** (academic) — a multi-agent pipeline whose Business Rule
  Inventory attaches to each extracted rule an exact `source_location`
  (`file:line`) and a confidence score, with a Behavioral Specification Graph as
  an inspectable trust boundary. Nearly the same architecture as a working
  code-to-spec tool — evidence the design is the field's consensus, not a
  one-off.
- **IBM watsonx Code Assistant for Z** — COBOL→Java translation with a tuned
  model. A code→code approach; verification is test-based, not
  citation-based — the contrast that motivates the code→spec→code detour.
- **Mechanical Orchard (Imogen)** — "behavior-first": reconstructs system
  behavior from observed production I/O rather than parsing source. The honest
  challenge to any static approach (see [limits](05-limits.md)).

## What the research says out loud

- **Grounding beats scale.** Feeding a model deterministic facts (control/data
  flow, resolved symbol definitions) measurably improves code understanding;
  bigger context windows don't substitute for it.
- **Citation-grounded generation is a recognized anti-hallucination pattern** —
  requiring outputs to cite specific source, checked mechanically, is not a
  local invention.
- **Adversarial / separate-context review** — having one model's output
  challenged by another in a fresh context is the technique Bun leaned on and
  that shows up across the verification literature.

## Where a working tool tends to be ahead

Most of the field does *one* of these. The leverage is doing them *together, in
one deterministic loop*: mechanical facts **and** mandatory citations **and**
EARS **and** adversarial refutation **and** repair **and** drift — language-open,
resumable, byte-deterministic. The pieces are known; the integration and the
hard-won [failure-mode knowledge](03-failure-modes.md) are the moat.

→ Next: **[Honest limits](05-limits.md)**
