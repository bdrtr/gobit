# ADR 0006 — How workflows reach the modules

- **Status:** Accepted
- **Date:** 2026-08-23
- **Phase:** 5

## Context

Up to Phase 5 the only form of cross-module access was **reading**, and it was
solved through the Query layer (ADR 0004). The cart flow changes that:
`calculate_totals` has to **call the service** of the `pricing` module to have a
variant's price computed; `complete_cart` (Phase 6) will call
`inventory.Reserve` and `payment.Authorize`. So for the first time a cross-module
**write/call** is needed.

Plan Section 2.5 says where it is written: *"A rule belonging to a single module
goes in the module service; a flow touching several modules is written in a
workflow."* But **how** the workflow is to reach those modules is not stated.

`internal/workflows` is not the core (Principle 2.4 does not bind it) and is not
a module either (the depguard rules are for `internal/modules/*`). So no
existing rule constrains it — it could import the modules directly.

## Alternatives considered

**A. Let workflows import the modules directly.** The shortest path. But
`internal/workflows` turns into a single node that knows every module: taking a
module out into a separate service, or changing it, breaks the workflows at
compile time, and workflow tests require a real module setup (and therefore a
database).

**B. A shared contract package in the core.** The very solution rejected for
modules in ADR 0001; it is rejected here for the same reasons (the god-package
tendency, the contract drifting apart from the implementation).

**C. Consumer-side interface + resolution from the container by name.** Applying
ADR 0001's pattern to workflows: the workflow declares the NARROW surface it
needs in its own package, and resolves the concrete service from the container
by name.

## Decision

**Alternative C.** `internal/workflows` does NOT import the modules either.

```go
// internal/workflows/cart/totals.go
package cart

// PriceCalculator is the ONE capability the cart total needs from the pricing
// module. The pricing package is NOT imported; the concrete service is resolved
// from the container under the name "pricing.service" and satisfies this
// interface structurally.
type PriceCalculator interface {
    CalculatePrice(ctx context.Context, priceSetID string, params CalcParams) (Money, error)
}
```

Consequently: an `internal/modules/...` import under `internal/workflows` is
FORBIDDEN, and this rule is checked automatically by the `internal/arch` tests —
just as with the isolation between modules.

## Consequences

**Positive**

- Workflows can be tested without a real module: a fake of the narrow interface
  is a few lines, and no database is needed. Exercising a saga's compensation
  requires blowing up a step, and that is only practical with a fake.
- Taking a module out into a separate service does not break the workflows at
  compile time; only the registration in the container changes.
- What a workflow actually wants from a module is readable from the surface of
  the interface it declares. It is visible that it depends on `CalculatePrice`,
  not on the whole of `pricing.service`.

**Negative / the price**

- A mismatch is caught not at compile time but at the moment of resolution from
  the container. What makes up for it is the diagnosable type-mismatch error of
  ADR 0002: the message writes both the registered concrete type and the
  expected interface. On top of that an integration test running against the
  real modules is MANDATORY for every workflow.
- The same concept (money, quantity, identity) is declared separately in the
  workflow and in the module packages. That is the accepted price of isolation
  (see ADR 0001).

## Related

- Plan Sections 2.4, 2.5, Section 4 (`/internal/workflows`), Phase 5, Phase 6
- [ADR 0001](0001-modul-arasi-iletisim.md) — the source of the pattern
- [ADR 0004](0004-query-veri-erisimi.md) — the read path's counterpart
