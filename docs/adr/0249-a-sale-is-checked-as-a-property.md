# ADR 0249 — A sale is checked as a property

**Summary:** Property-based tests draw inputs on every ordinary run; the first
take every cart a shopper can build through to its document, and hold each
copy of the money arithmetic to arbitrary precision.

- **Status:** Accepted
- **Date:** 2026-09-30

Measurement: [measurements/0249](../measurements/0249-a-cart-drawn-a-hundred-times.md)

## Context

Nothing in the tree drew its inputs on an ordinary run. The fuzz targets run
their seeds in CI and generate only on demand (ADR 0080), and ADR 0080 rejected
a target over the discount engine because its generator is where the
assumptions would hide. D169 was three modules each holding a line's subtotal to
a rule one of them had changed; every test built its own inputs for its own
module, and none took a drawn cart through the others. The multiplication is
written four times, and two of the copies let a zero price through with a
negative quantity (D172).

## Decision

Property-based tests use `pgregory.net/rapid` in the ordinary lanes, a hundred
draws each and a new seed on every run. A property draws only what a contract
admits or a shopper can do, and asserts what the contract states: the first
hold every cart a shopper can build to its order, payment and invoice, and each
copy of the money arithmetic to arbitrary precision.

## Consequences

- The sale's property draws a market, variants and quantities, and everything
  between the draws and the assertions is the real wiring, so no generator
  stands in for a module. It fails with D169, D170 or D171 put back, and takes
  about six seconds for a hundred carts in the integration lane.
- A failing property prints its draws, shrunk, and writes them under
  `testdata/rapid/`, which is ignored. A new seed each run can find what an
  earlier run missed; that red is a counterexample, not a flake.
- Amounts are drawn from six classes equally, zero and negatives among them:
  a hundred draws of a skewed mix found D172 in one module and missed it in the
  other.
- `rapid` was already in every embedder's module graph at v1.2.0, through the
  Docker API module testcontainers requires; the require moves it to v1.3.0 and
  to a line with its reason.
- The cart's and the order's multiplication check the sign before the shortcut
  for zero.

## Rejected

- **`testing/quick`.** It is frozen, does not shrink, and its generators cannot
  be scoped to a contract.
- **Fuzz targets for the same properties.** The ordinary lane runs only their
  seeds (ADR 0080), and a property that runs when somebody fuzzes is not run.
- **A fixed seed.** The same hundred draws on every run are a table.
