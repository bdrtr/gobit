# ADR 0110 — A refused promotion says why, and only to the operator

**Summary:** The admin computation reports which promotions were considered and
left out, each with a typed reason, read from a candidate query WITHOUT the
status filter. It costs a second read and buys an answer to "I typed the code and
nothing happened".

- **Status:** Accepted
- **Date:** 2026-09-10

## Context

`eligible()` returned a bool and dropped the reason. Nine gates fed one
`false`, so the computation could say WHAT applied and never why the coupon a
merchant published did not: they saw a discount of zero and had nine hypotheses.

ADR 0109 made the question routine. Customers can now type codes, so support
tickets that begin "the code does nothing" are the ordinary case rather than a
curiosity.

The obstacle was the candidate query. `ListApplicablePromotions` carries
`status = 'active'`, so a promotion that was published but never activated is
not a candidate at all: it comes back neither applied nor skipped. That is the
commonest cause of a code doing nothing, and it was the one answer the endpoint
could not give.

## Decision

`ComputeResult` gains `Skipped`, one typed `SkipReason` per candidate the
elimination refused. `skipReasonOf` is now the single place the elimination is
decided and `eligible` is its yes/no.

`ExplainDiscounts` reads candidates WITHOUT the status filter and runs the same
computation; the admin endpoint calls it and publishes `skipped`.

## Consequences

The amounts of the two paths are IDENTICAL and have to be: the merchant is shown
the numbers from one and the customer is charged the numbers from the other. It
holds because the wider read's extra members all fail the elimination, and it is
pinned by tests on both the fake and the real schema.

`skipped` is published on the ADMIN endpoint alone. It is the one field where
that body and the cross-module body differ, and the difference is the point:
telling a customer their code exists but its campaign has not started hands a
code guesser a campaign calendar. The cart flow has no use for a reason either,
and a field with no consumer is refused (ADR 0009).

The reasons are a CLOSED SET, and the vocabulary is written out a second time so
the cover test can fail in both directions — Go cannot enumerate the members of a
named string type. A word nobody can produce is an answer the endpoint promises
and never gives; that is not hypothetical, it is what the first version of this
change did with `not_active` before the query was widened.

Three campaign states — deleted, out of window, budget spent — are ONE word.
They are the same answer to the merchant, and splitting them would put a
campaign's calendar into a response.

The population is one cart's worth: every automatic promotion and the promotions
of the codes that were sent. A coupon nobody typed stays out, so the answer does
not grow with the promotion table.

## Rejected

**Dropping the status filter from the hot query.** A cart's totals are recomputed
on every change, and that filter is what keeps the read proportional to the
promotions that could apply.

**A free-text explanation.** An operator cannot filter, count or alert on a
sentence. "Eleven carts this hour were refused for currency_mismatch" is a
question prose cannot answer.

**Publishing it on the storefront.** The reason would reach the customer whose
code was refused, which is the leak the coupon lookup already refuses to open.

**Naming every promotion in the shop.** The answer's size would grow with the
catalog while saying nothing more about the cart that was asked about.
