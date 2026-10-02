# ADR 0334 — The panel writes a shipping option

**Summary:** The Shipping options screen writes an option under
`fulfillment:write` on a provider this installation registers and one of the
newest shipping profiles, offered in a region and its currency or in every
region and a currency typed, through `fulfillment.admin`.

- **Status:** Accepted
- **Date:** 2026-10-02

## Context

The panel lists and revises the shipping options (ADR 0333), but a new
carrier, or a new region the shop starts selling in, still took the admin
API to write an option, its caller having to know the provider's identifier
and the profile's.

## Decision

The fulfillment module's surface lists the providers it registers and its
newest shipping profiles, and writes an option through the service the API
writes it through, so it is refused where the API is. The screen's form
offers those and the shop's regions, and an option offered in a region
charges in that region's currency.

## Consequences

- A merchant opens a carrier's option where the options are listed, choosing
  among the providers that can carry it rather than typing an identifier.
- A fee is typed in the currency's decimals and read in the currency the
  option charges in, so a region's option cannot charge in another currency
  by a slip of the keyboard.
- The form offers the hundred newest profiles and says when there are more;
  a shop with more profiles writes an option on an older one through the
  API.
- A region's name is what the storefront already publishes, so the form
  reads it under the fulfillment module's privilege (D208).
- Shipping profiles, and an option's rules, stay on the admin API.

## Rejected

- A profile typed by its identifier: an operator does not know it, and a
  wrong one is refused only after the form is sent.
- Taking the typed currency for a region's option: the region prices its
  carts in one currency, and an option in another is offered to no cart.
