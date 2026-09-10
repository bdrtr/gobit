# ADR 0125 — Serving an unverified customer claim is a choice

**Summary:** The four storefront routes that served a customer claim unchecked
when no verifier is bound now refuse it, and the old answer is one setting away.
It costs three tests and a smoke scenario, and buys an installation that cannot
leak by omission.

- **Status:** Accepted
- **Date:** 2026-09-10
- **Amends:** ADR 0057, whose third decision paragraph — "a bound identity is
  required nowhere it was not required before" — this record replaces. The rest
  of that record stands, including the one comparison it built.

## Context

ADR 0057 put every storefront surface naming a customer on one comparison and
chose, deliberately, that an installation binding no verifier keeps four of them
serving the claim unchecked. It rejected refusing for a reason that was not
wrong: it withdraws a working b2b storefront and every customer cart from an
embedder who did nothing wrong.

The residue was stated in the open and carried in `docs/known-limits.md`: a
caller who knows a customer identifier — which travels in every order response —
reads that person's employer and spending limit and opens a cart in their name.
The cart half spends their B2B allowance.

What the record could not do is make that a decision. An installation got the
open answer by not knowing the question existed, and a WARN at startup is not a
choice.

## Decision

The four routes refuse an unverified claim by default, exactly as the address
book already does. `STOREFRONT_TRUST_UNVERIFIED_CUSTOMER_CLAIM` restores the old
answer for an installation that asks for it by name.

## Consequences

Nothing is withdrawn from an embedder who wants the old behaviour; what is
withdrawn is getting it without deciding. This repository already made that
argument about GraphQL introspection, and it reads the same backwards: the
existence of the switch makes being open a decision rather than an accident.

The default is the ZERO value, and the field is named so that it is. `internal/e2e`
mirrors the composition root with zero Options, and so does an embedder
assembling the modules by hand; a negatively named field would have made every
one of those the open answer.

One field in the composition root feeds both modules, because two answers to one
question is the divergence ADR 0057 built a shared comparison to prevent.

Guest traffic is untouched by either value, which is the sentence that whole
record was built around: a body naming nobody is never asked.

It is a breaking change for an installation that binds no verifier and relies on
those four, and `0.x` is where that is allowed. The measured cost inside the tree
is two witnesses and one smoke scenario — all three of them tests ADR 0057 wrote
to pin the behaviour it chose.

Binding a verifier is still the real answer, and this record does not make a bad
one detectable: an implementation handing the claimed identifier back satisfies
the interface and closes nothing while looking closed.

Measurement: [measurements/0125](../measurements/0125-what-an-unverified-claim-still-buys.md)

## Rejected

**Leaving it as ADR 0057 decided.** The residue was documented and unactionable:
an operator learns of it by reading a limits file, and the installation that most
needs to know is the one that read nothing.

**Refusing with no way back.** It is ADR 0057's rejected option with its reason
unanswered, and that reason still holds.

**A per-module setting.** Two answers to one question, which is the shape ADR
0057 spent a shared comparison to avoid.

**Binding a permissive identity instead of a flag.** A "trust anything"
implementation of a published interface is a footgun that outlives the decision
that installed it.
