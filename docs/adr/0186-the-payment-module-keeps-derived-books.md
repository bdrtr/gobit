# ADR 0186 — The payment module keeps derived books

**Summary:** The payment module publishes its movements as a double-entry
journal, derived when read from the captures, refunds and ledger grants it
already keeps. It costs a chart of accounts to maintain and four time indexes,
and it cannot disagree with the payments it describes.

- **Status:** Accepted
- **Date:** 2026-09-25

Measurement: [measurements/0186](../measurements/0186-books-read-from-the-rows.md)

## Context

An accountant closing a period asks where the money is: what the providers
hold, what the shop owes in store credit and points, and what was refunded.
gobit kept every movement in rows it never deletes — captures, refunds and two
balance ledgers — and none of them was a book. The feature list proposed a ledger table
written beside every movement. ADR 0119 refused that shape for the order
module: a second record of the same money can disagree with the first. ADR
0042 left the shape of a settlement row to its first consumer, and there is
none yet.

## Decision

`GET /admin/v1/payment-journal` derives one balanced entry from each capture,
refund, store credit grant and loyalty grant in a window, with six accounts
and the provider and customer as dimensions. Nothing is written: the entries
are a reading of the existing rows, and a window over 93 days or 10,000
movements is refused rather than cut.

## Consequences

The chart is one table in `service/journal.go`. A capture debits the account
its money came from and credits receivable; a refund reverses it. A capture
through store credit or points reduces what the shop owes the customer, and
any other provider's capture is money in that provider's clearing. A store
credit or loyalty grant moves the liability against its cost.

Holds and releases are not entries. They reserve a balance a tender may spend,
and the capture and the refund move the money. The journal's store credit and
loyalty accounts are therefore what the shop owes, and the ledgers' sums are
what a customer may spend, and the two agree whenever no session is pending.

`receivable` runs negative on these books alone. Its debit side is the order
that created the obligation, with its revenue, tax and shipping, and that is
the order module's to publish in a slice of its own.

Every entry is built as one debit and one credit of the same amount, and the
tests hold the output to it. Unbalanced books would have to come from a movement
the chart cannot read, and that is an error rather than an entry.

Four indexes serve the window, so a read no longer scans every row the module
ever wrote. The two on the ledgers are partial on the kinds the journal reads.

A settlement row for a spread between what the customer pays and what the shop
receives is still ADR 0042's to shape. The journal reads movements, so such a
row becomes an entry by being read, not by a second write.

## Rejected

- **A journal table written beside every movement.** It is the mirror ADR 0119
  refused, and it would record each movement twice.
- **Replacing the payment tables with journal rows.** It rewrites the module's
  write path and every reader of it for a record the rows already imply.
- **Holds as entries.** They move no money, and the books would swing with
  every checkout that is abandoned.
- **Cutting a window at the bound.** A journal missing its tail still balances,
  and balanced books that miss money are worse than a refusal.
