# ADR 0100 — The customer sees the goods, not the ledger

**Summary:** The storefront gets the order timeline, narrowed to the moments
about the order and the goods; the money moments and the archiving do not cross.
It costs a second response type that cannot hold a figure.

- **Status:** Accepted
- **Date:** 2026-09-09

## Context

`GET /admin/v1/orders/{id}/timeline` composes everything that happened to an
order out of records that already exist, and it was built for the support desk.
The customer waiting for a parcel had no equivalent: the storefront carried the
order read and the return request and nothing that answers "where is it".

The timeline could not simply be published. It carries the capture and refund
amounts, which are the merchant's ledger view, and `order.archived`, which is
the merchant filing the order away.

## Decision

`GET /store/v1/orders/{id}/timeline` returns the SAME composition, filtered to
the kinds about the order's own lifecycle and about the goods. The money moments
and the archiving are excluded, and the response type carries no amount field at
all.

## Consequences

The narrowing has two halves that fail separately. `customerVisible` decides
WHICH moments cross and is proved on its own; the storefront DTO decides what
SHAPE crosses, and having no money field means a later edit that copied one
would not compile. An end-to-end test binds the two by asserting the storefront
answer on a real order that really has a capture.

A kind added tomorrow is INVISIBLE on the storefront until somebody puts it in
the set. That default is the safe direction: an unclassified moment reaching a
customer is a leak, while a missing one is a gap somebody notices.

The clock field IS published to the customer. The moments do not share one axis,
so two entries a second apart may be ordered by different clocks; hiding that
does not make the order true, it makes it unexplainable.

The route names an ORDER, so it inherits the boundary the storefront order read
declares: knowing the id is the capability, and whether the caller may see it is
the embedding application's decision (ADR 0008). It is outside ADR 0057's
population for the same reason that read is — there is no customer claim in the
request to compare.

## Rejected

- **Publishing the admin timeline as it is** — it states amounts the customer
  will reconcile against their bank, where a partial capture and a refund's
  recorded figure are not the numbers that land.
- **Reusing the admin type and zeroing the money fields** — a blanking step can
  stop being called; a type without the field cannot.
- **Composing a second timeline for the storefront** — a second place for a
  moment to be forgotten, and the two would drift.
- **Hiding the clock** — the ordering is genuinely across clocks, and an
  unexplained out-of-order pair is worse than a labelled one.
