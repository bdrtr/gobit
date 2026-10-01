# ADR 0283 — Production registers no manual provider

**Summary:** The manual payment provider, which authorizes and captures whatever
the caller names, is registered everywhere but `APP_ENV=production`; a module
built with the zero options registers it nowhere.

- **Status:** Accepted
- **Date:** 2026-10-01

## Context

The payment module registered its manual provider in every installation. The
provider exists so that a first run and the test lanes take an order end to end
without a provider account: it authorizes the amount it is asked for and captures
it, and a key in the session's data names a decline. The storefront lists every
registered provider and takes any of them at checkout, so a shopper of a
production shop could complete a cart with `manual` and receive an order the
payment module records as captured in full, with nothing paid (D194). No
setting removed it, and the production guards in the configuration never named
it.

## Decision

The payment module's `Options.ManualProvider` decides whether the manual
provider is registered, and its zero value registers none. The composition root
sets it to `APP_ENV != production`, so development and staging keep it and
production never has it.

## Consequences

- A production installation lists and accepts only the providers it registered
  through a plugin, the gift card and, where the claim is proven, the two
  balance tenders; a cart that names `manual` is refused at the plan, before an
  order exists, with the payment module's not-registered error.
- A production installation with no provider plugin takes no order paid by a
  provider — which is the true state of a shop without a provider account.
- A production database that holds manual sessions keeps them; reconciliation
  counts them as unaskable and a refund through them is refused, since they
  never moved money.
- An embedder who builds the payment module by hand opens the manual provider
  only by asking for it, as with the balance tenders.

## Rejected

- Keeping the provider and refusing it at the storefront only: the admin's
  payment routes would still capture what it is told, and the list a shopper
  reads would still name it.
- Leaving it out of staging too: staging is where an installation walks the
  order flow before its provider account exists.
- A separate setting to enable it: a production switch for a provider that pays
  for nothing is a switch nobody should turn.
