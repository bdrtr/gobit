# ADR 0237 — A withdrawn replacement gives back what it held

**Summary:** Withdrawing a replacement goes through the returns flow, which
releases the units its lines set aside before the record is withdrawn, and
refuses when those units already left for a parcel.

- **Status:** Accepted
- **Date:** 2026-09-29

Measurement: [measurements/0237](../measurements/0237-a-promise-nobody-could-name.md)

## Context

A dispatch sets each line's units aside and writes the promise on the line
before it opens the parcel (ADR 0090), so a dispatch refused on a later line, or
stopped at the parcel, leaves promises for its retry. The withdrawal wrote the
record alone: the order module cannot reach the inventory, no other path
released a replacement's promise, and the flow's godoc said such a promise
"expires with the record", which nothing did (D159). Bundles are about to hold
one promise per part.

## Decision

`POST .../replacements/{replacementId}/cancel` calls the returns flow, which
releases every promise the replacement's lines name and then withdraws the
record, and fails closed without the flow. A promise the inventory refuses to
release as confirmed refuses the withdrawal, since its units left for a parcel.

## Consequences

- A withdrawn replacement holds nothing. The endpoints answer as before, plus
  409 `returns_workflow_stock_not_released` when a promise will not go back.
- Units a dispatch confirmed before it died recording its parcel keep the
  replacement open; dispatching it again finishes it, as the flow always said.
- A release that fails leaves the record open, and asking again finishes the
  withdrawal: both steps repeat safely.
- The order interop gains `CancelReplacement`, which writes the record only.

## Rejected

- **The record withdrawn first, the promises released after.** A promise that
  turned out confirmed would then belong to a withdrawn record with its goods in
  a parcel.
- **Promises that expire.** The inventory has no clock on a reservation, and one
  for replacements alone would release units a slow dispatch still needs.
- **An endpoint that releases a reservation by id.** It would move the knowledge
  of which promise is whose from the record to whoever calls it.
