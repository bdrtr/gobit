# ADR 0402 — An experiment runs outside gobit

**Summary:** gobit assigns no arm, records no exposure and computes no stopping rule; an experiment product keyed on the embedder's visitor hears gobit through the bus.
It reopens when a flow in this tree must act by arm or the panel must show an arm's result.

- **Status:** Accepted
- **Date:** 2026-10-05

Measurement: [measurements/0402](../measurements/0402-who-would-run-an-experiment.md)

## Context

Feature row C10 asks for a statistical guard on experiments: a stable key per
visitor, a seeded assignment drawn from it, a record of each exposure and a
threshold that does not stop on a lucky look. gobit holds no such key. The
publishable key names a sales channel and is no secret (ADR 0008);
`contrib/identity-session` proves a signed-in shopper and nobody else (ADR
0127, ADR 0366); gobit issues no customer identity (ADR 0043); and a key that
follows an anonymous visitor is a tracking identifier whose consent is the
embedder's (ADR 0029).

What an experiment product counts is published already. `cart.created` and
`cart.completed` carry the cart id and `order.placed` the total; the
completion response and the order read carry the order id, the cart id and the
total together. The row names its consumer as the first checkout variation
experiment, and the tree has none.

## Decision

**gobit assigns no arm, records no exposure and computes no stopping rule; an
experiment product keyed on the embedder's own visitor hears gobit through the
bus. This reopens when a flow in this tree must act differently by arm, or the
panel must show an arm's result.**

## Consequences

- **The visitor key, the assignment and the consent are the embedder's.** An
  anonymous visitor gets no identifier from gobit.
- **A guest's conversion joins on the bus; its revenue joins through a read.**
  `cart.completed` carries no order id and `order.placed` no cart id, so a
  subscriber reads the order, or the storefront keeps what completion answered.
- **A price that differs between shoppers is the merchant's.** A group,
  contract or list price charges whom the operator put in it (ADR 0049,
  ADR 0185). An embedder that fills groups by arm runs a price experiment the
  order line records (ADR 0168); gobit neither assigns it nor tells the shopper
  the price is personalized, and ADR 0167's reference price matches no ruled
  price.
- **The cart's metadata carries no arm to a price** (ADR 0403).
- **No sequential threshold ships.** A stopping rule belongs beside a screen
  that shows a result, and there is none.

## Rejected

- **An experiment module now.** No flow varies by arm and no screen shows a
  result; the bar is a consumer in the tree (ADR 0390).
- **A visitor cookie issued by gobit.** It is identity gobit does not issue
  (ADR 0043) and consent it does not own (ADR 0029).
- **The cart's metadata as the arm.** The storefront's caller writes it.
- **`cart_id` on `order.placed` now.** No subscriber in the tree reads it.
- **An exposure topic on the outbox.** The server varies nothing by arm, so
  every exposure happens in the storefront.
- **Reopening when an installation runs an experiment.** That consumer is
  outside the tree, and the bus already serves it (ADR 0390).
