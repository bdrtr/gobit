# ADR 0448 — A back-in-stock alert is on the bus

**Summary:** When the stock alert flow hands a customer's back-in-stock mail to the notification module, it publishes `wishlist.back_in_stock` with the customer, the variant, its product and the mark's armed moment, for a consumer outside gobit that `subscriberlessPublications` names.
It costs a topic no subscriber inside gobit acts on, kept published, and buys the consumer's push after the mail without reading the notification module's rows.

- **Status:** Accepted
- **Date:** 2026-10-17
- **Amends:** [0215](0215-a-wishlist-item-can-ask-for-its-stock.md), whose mail was the alert's one trace

## Context

depremtek's backend, a consumer running in gobit's process, pushes a
back-in-stock alert to the customer's devices after gobit mails it (D303).
Nothing published the alert, so it polled `notification_deliveries` for the
template `wishlist.back_in_stock` and parsed the customer and the variant out
of the delivery's reference `customer:variant:armed`, a table and a format
no record publishes.

## Decision

The stock alert flow publishes `wishlist.back_in_stock`, carrying
`customer_id`, `variant_id`, `product_id` and `armed_at`, after the
notification module takes the mail and before the mark is cleared, under an
id derived from a digest of the mail's reference. `webhook-out` forwards it
with `customer_id` withheld, and `subscriberlessPublications` names the
consumer and this record.

## Consequences

- A publish that fails leaves the mark armed; the next pass's mail is the notification module's repeat, which sends nothing, and its event carries the same id, so a subscriber keyed on the id acts once.
- The event says the mail was handed over, not delivered: a failed delivery is the notification module's ledger's, as before.
- It carries no locale: no customer record holds one. A push renders in each device's own (ADR 0446).
- It is published on the bus and not through an outbox: the flow owns no transaction, and the armed mark is what is retried.
- The price-drop mail publishes nothing; no consumer asked.
- `webhook-out` forwards seventeen topics.

## Rejected

- Publishing the notification module's delivery table and the reference's format: a ledger's columns promised to a reader.
- The web-push plugin subscribing and pushing the alert itself: ADR 0386 keeps its own trigger to one topic, and the push's copy is the consumer's.
- `customer_id` withheld from the bus too: the bus stays inside the installation, and the subscriber pushes to that customer.
- An id of the customer and variant in clear: it reaches a webhook receiver the customer is withheld from.
