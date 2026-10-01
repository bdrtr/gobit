# ADR 0311 — The panel lists the promotions

**Summary:** The promotion module registers a panel surface, `promotion.admin`,
that lists the promotions in one status with their usage and limit, and the
panel's Promotions screen shows them under `promotion:read`. The read provider
keeps returning only active promotions without their usage.

- **Status:** Accepted
- **Date:** 2026-10-01

## Context

The panel had no screen for promotions: an operator who wanted to know which
coupons were live, which drafts waited or how often a code had been used read
the admin API. The promotion module's read provider returns only active
promotions and leaves the usage out, because the read layer cannot tell a
storefront from an operator and a draft's code or a coupon's use count is not
for a shopper. The inventory module had met the same shape for stock levels by
answering the panel through its own surface (ADR 0013).

## Decision

The promotion module registers `promotion.admin`, whose `PromotionsJSON` lists
the promotions in a status — draft, active or inactive — a page at a time with
the code, the type, whether it applies itself, the usage and the limit, and
the status's total. The panel's Promotions screen lists one status at a time,
the active ones first, under `promotion:read`.

## Consequences

- An operator sees the live coupons, the drafts waiting and how far each is
  from its limit without an API client.
- The read provider keeps its rule: nothing reachable by a storefront lists a
  draft or a usage count.
- The list is read-only; a promotion is created, changed and switched over
  the admin API, as before.
- The listing keeps the admin API's order, the order the promotions were made.
- The promotion module's file that registers its surfaces is translated to
  English and leaves the language ledger.

## Rejected

- A status filter and usage fields on the read provider: a storefront could
  then list a draft's code and a coupon's use.
- Reading the admin API from the panel: the panel's server-rendered screens
  read through module surfaces until ADR 0030's migration reaches them.
