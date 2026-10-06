# ADR 0427 — A cart keeps no past, and gobit files no dispute

**Summary:** A cart is overwritten in place and keeps no history, and gobit assembles no dispute file beyond the order's reads.
It costs a cart read at a past moment and a one-call chargeback file, and keeps gobit from storing a buyer's IP or a second copy of every cart write.

- **Status:** Accepted
- **Date:** 2026-10-06

## Context

An order can be read as it stood (ADR 0171), with the address and delivery in
force (ADR 0411), and its timeline tells every movement (ADR 0170). A cart is
updated in place and an abandoned one is deleted on the shop's period (ADR
0301). A dispute asks for more than the order: the buyer's IP, the carrier's
checkpoints and what the customer was sent. gobit keeps none of them: ADR 0149
left a checkpoint history to a decision of its own, and the notification
module's delivery log holds a template and a reference but no recipient
address and no body (ADR 0029).

## Decision

A cart keeps no history: what it held before checkout is not recorded, and an
order keeps what was sold. gobit assembles no dispute or chargeback file; the
order read, its as-of read, its timeline, its documents and its parcels are what
it knows.

## Consequences

- A cart cannot be read at a past moment; the order's lines and prices are the
  sale.
- No table holds the buyer's IP, a carrier checkpoint history or a sent
  message's body or recipient.
- An embedder answering a chargeback reads the order, its as-of read, the
  timeline, the invoices and the parcels, and adds its own edge logs.
- The bus carries `cart.created` and `cart.completed` (ADR 0153) for an
  embedder that keeps more of a cart's life itself.
- This reopens when a payment provider plugin reports a dispute to gobit, which
  then decides the dispute record and the evidence it asks for, or when a
  consumer inside gobit needs what a cart held before it became an order.

## Rejected

- A cart history table: a second copy of every cart write, for no reader.
- A dispute endpoint over the existing reads: a surface with no consumer (ADR 0390's bar).
- Storing the buyer's IP: personal data whose retention is the embedder's (ADR 0029).
- A carrier checkpoint history: ADR 0149 left it to a decision of its own, with polling and deduplication.
