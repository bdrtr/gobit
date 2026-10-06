# ADR 0418 — A counter sale is an operator's cart in a store's channel

**Summary:** A shop sells at its counter through an operator's cart in the store's sales channel, completed with an offline method and recorded under that channel; gobit ships no till.
It costs a counter two acts per cash sale and no terminal, drawer, receipt or change, and keeps one checkout for every sale.

- **Status:** Accepted
- **Date:** 2026-10-06

## Context

Feature row B3.4 asks for a point of sale. A store is a sales channel bound to
its own warehouses (ADR 0092). An operator opens a cart in a channel (ADR 0146,
ADR 0305) and completes it with the total it confirmed (ADR 0286); neither a
shipping address nor a delivery is required, which
`TestACounterSaleNeedsNoAddressOrDelivery` holds on the production wiring. Cash
is an offline method: the completion authorizes it and the order owes until the
panel records the money (ADR 0284, ADR 0287). Since ADR 0410 the order records
the channel it was sold in, so a store's sales are listed apart from the web's.
ADR 0286 kept card providers from the operator, whose hands the shopper's card
data would pass through; a card terminal is card present and the shopper's own
device, and no provider for one exists. No till client, terminal plugin or
pickup storefront exists in this tree.

## Decision

**A counter sale is an operator's cart in the store's sales channel, completed
with an offline method and recorded under that channel; gobit ships no till
surface, no card-terminal provider, no channel type and no pickup location. This
reopens when a till client in this repository, the panel included, must record a
fact of the counter that no order, payment or channel carries.**

## Consequences

- A cash sale is two acts, the completion and the recorded payment; the order
  owes in between, as every offline method's does.
- No terminal, shift or drawer is recorded, the tendered amount and the change
  are not, and gobit prints no receipt; the order's `placed_by` names the
  operator.
- A card at the counter is taken on the shop's own terminal and recorded as an
  offline method's payment.
- In-store pickup is a shipping option the operator names, priced zero and
  fulfilled by the manual provider; the option carries no store location.
- Store and web sales are told apart by the channel the order recorded.

## Rejected

- **A till screen in the panel now.** No client asks for a terminal, shift or drawer fact.
- **One act for a cash sale.** Capturing at the completion is what ADR 0284 refused.
- **A card-terminal provider contract.** Nothing implements one (ADR 0390).
- **A channel type (store or web).** The recorded channel answers every reader in the tree.
- **A pickup location on the shipping option.** No storefront in reach offers pickup.
