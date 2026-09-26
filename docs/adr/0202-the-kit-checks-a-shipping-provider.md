# ADR 0202 — The kit checks a shipping provider

**Summary:** `core/providertest.Fulfillment` opens a shipment with a
destination made of markers and reports a provider that returns any of them, one
that opens a second shipment for a repeated key, and one whose second cancel
fails. It costs the provider a stub or a sandbox in its test, the first suite in
the kit to need one.

- **Status:** Accepted
- **Date:** 2026-09-26

Measurement: [measurements/0202](../measurements/0202-a-carrier-that-keeps-the-address.md)

## Context

ADR 0194 handed a carrier the order's shipping address and forbade it to return
the address in the data the parcel stores, since the parcel is not erased with
the person. It recorded that nothing checked a plugin for that, and that the
conformance kit did not cover shipping. The kit (ADR 0077) checked only what
holds without an upstream, and the box provider ran the identity check alone.

## Decision

`providertest.Fulfillment` creates a shipment through the provider with a
destination whose every field is a marker and a key of its own, creates it
again, and cancels it twice, reporting any marker in the returned data, a
second shipment and a failed second cancel. A gate holds every in-tree provider
whose methods take a contract's input to that contract's suite.

## Consequences

This is the first suite that calls a provider for real, which ADR 0077 kept the
kit from doing. A shipment's returned data can only be read by opening one, so
the caller hands in a provider that can: against a stub of the carrier's
service, as its own tests usually run, or a sandbox. The suite still checks
what the provider answers and not what it sent the carrier.

It finds the address as it was given. A provider that returns it upper-cased,
encoded or hashed is not caught. The country is a real code rather than a
marker, since a carrier may refuse anything else.

The box provider runs it on its in-memory ledger and passes. `Fulfillment` is
a published name kept until 1.0.0 (ADR 0026).

The gate matches a provider to a suite by the input its methods take —
`CreateFulfillmentInput` to `Fulfillment`, `ClassifyInput` to `Classifier` — so
a shipping provider that ran the identity check alone now fails it, as the box
provider would have.

## Rejected

- **A stub carrier inside the kit.** The rules are about the provider's own
  answer, and a stub standing in for the provider would check the stub.
- **Checking what reaches the carrier.** The label needs the address, and the
  kit cannot see the provider's request without owning its transport.
