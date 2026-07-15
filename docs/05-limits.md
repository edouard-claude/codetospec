# 5. Honest limits

Saying what code-to-spec *can't* do is part of the product. A tool that claims
100% is lying, and lying is exactly the failure the discipline exists to avoid.
Four limits worth stating to any stakeholder up front.

## 5.1 Static extraction cannot see runtime behavior

You extract the rules *written in the code*, not the behavior *observed in
production*. Configuration-driven logic, emergent behavior across services,
timing, and anything decided at runtime by data are partially or fully
invisible. This is Mechanical Orchard's whole thesis, and it's right: a
behavior-first approach (test harness from real I/O) catches things a parser
never will. Static code-to-spec is a different, complementary instrument — say
so, don't paper over it.

## 5.2 The semantic guarantee has a ceiling

There are three levels of spec↔code coherence, and only one is 100%:

- **Structural (100%)** — every rule cites lines that resolve to real code.
  Guaranteed by verification.
- **Semantic (a fraction)** — the cited lines actually *prove* the rule, as
  judged by an adversarial reviewer. On real runs this lands well short of
  100%, and that's honest: not everything the map extracts is a clean, provable
  business rule, and the reviewer is deliberately strict.
- **Temporal** — the spec stays true as the code changes. Guaranteed by drift
  detection, *if* you re-run.

The value is not pretending the semantic level is 100% — it's **telling the
reviewer which third is uncertain** so attention goes where it's needed.

## 5.3 Completeness is not measured — yet

The tool reports what it *found*. It does not, by default, tell you how much
business logic exists with *no* rule attached — the code the spec silently
doesn't cover. Coverage of endpoints and entities is measured; coverage of
"unspecified business code" is the harder, more important gap, and the honest
frontier for "guarantee spec↔code coherence."

## 5.4 The human referent is not optional

The output is *for* a domain expert to validate — the person who knows that
"the Gol tonnages include Grand Pourpier." The tool's job is to make that
validation *possible and fast*: named rules, exact citations, a flagged
uncertain subset. It is not to remove the human. The engineering time doesn't
vanish; it moves — from deciphering period code to deciding what the product
must do.

---

That last sentence is the whole point. Code-to-spec doesn't replace judgment —
it relocates it to where judgment belongs.

← Back to the **[index](README.md)**
