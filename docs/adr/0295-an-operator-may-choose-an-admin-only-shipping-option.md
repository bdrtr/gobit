# ADR 0295 — An operator may choose an admin-only shipping option

**Summary:** The operator's doors — the admin cart API and the panel's
telephone order — list and accept the shipping options a shop marked
admin-only. The storefront's doors still neither list nor accept them.

- **Status:** Accepted
- **Date:** 2026-10-01

## Context

A shop marks a shipping option admin-only to keep it off the storefront: a
pick-up at the desk, a courier arranged by telephone. The fulfillment module's
interop takes `include_admin_only` for exactly the flows an operator runs, and
nothing passed it. ADR 0286 had put the storefront's shipping write behind the
admin route unchanged, and ADR 0292 listed for the operator what that write
accepted. An operator taking a telephone order therefore could not choose the
one kind of option made for them.

## Decision

The cart flow quotes and accepts an admin-only option when the caller is an
operator, and a return option for no one. The admin listing, the admin
shipping write and the panel's surface take the operator's path; the
storefront's listing and write keep the shopper's.

## Consequences

- An operator offers the caller the desk pick-up or the arranged courier, at
  the price the fulfillment module quotes for the cart.
- A shopper cannot reach an admin-only option through a cart an operator
  opened: the storefront's doors ask for none on any cart.
- The admin shipping write is no longer the storefront's handler; the two
  share one implementation and differ by the path they take.
- The admin-only flag is the shop's to set on the option, and an option set so
  by mistake is one only operators can choose.

## Rejected

- Deciding by who opened the cart: a cart an operator opened is still one a
  shopper can complete with its link, and the storefront's answer must not
  depend on it.
- A separate admin-only listing beside the cart's: the operator would choose
  from two lists priced by two paths.
