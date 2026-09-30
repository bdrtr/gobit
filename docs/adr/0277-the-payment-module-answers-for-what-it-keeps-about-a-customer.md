# ADR 0277 — The payment module answers for what it keeps about a customer

**Summary:** The payment module declares the customer on its collections,
ledgers and ledger sessions and the free text beside them, discloses a
customer's rows found by customer id, and answers an erasure as retained with
what it keeps and why.

- **Status:** Accepted
- **Date:** 2026-10-01

## Context

The payment module keeps a customer's store credit and loyalty ledgers, the
sessions that spend them, and payment collections that name the customer whose
cart they collect for; beside those sit a collection's metadata, what a
provider returned and why it declined, the replay key a caller chose and an
operator's reason for a refund or an issue of credit. It implemented none of
the personal-data capabilities, so the data-subject sweep never asked it: a
person's dossier held no balance and no payment, and an erasure report said
nothing of them. The order module declares the same customer id on an order.
A balance tender's decline sentence states the customer's balance.

## Decision

The payment module declares every column that names or describes a customer —
the customer id, a ledger entry's amount or points, a balance tender's decline
sentence — and the free-form values on those rows and on a collection's
sessions and refunds, and discloses the rows of a customer found by customer
id in one read-only snapshot. It answers an erasure as retained, rewriting
nothing, and names every declared column with the reason: a payment is the
shop's account of money that moved and a ledger is a balance owed to the
person.

## Consequences

- A dossier lists a customer's collections, each with the sessions, refunds
  and provider sessions it gathered as `<collection id>/<row id>`, then the
  store credit and loyalty entries and sessions.
- A subject with an e-mail and no customer id is unresolvable here: a guest's
  collection names nobody, so its metadata and a provider's reply cannot be
  attributed; an erasure of such a subject names nothing as kept.
- A collection, a session, a capture and a refund keep their amounts
  undeclared, as an order's totals are: they describe the sale.
- A gift card, its ledger and its sessions' own columns name nobody; a gift
  card session is disclosed for the caller's replay key alone.
- The collections, the provider sessions and the ledger sessions have no index
  on the customer or the reference, so an answer scans them; a request a
  person makes rarely does not earn an index every checkout would write.
- `internal/modules/payment/erasure_test.go` holds the declaration to every
  column of the schema through `internal/schemaaudit`, with the reason each
  undeclared one holds nobody.

## Rejected

- Declaring the customer id alone: the dossier would list a person's ledger
  rows without what they hold.
- Answering an erasure by anonymizing a ledger: the customer id is the only way
  the balance reaches its owner, and a balance with no owner is money the shop
  can no longer pay back.
- Finding a guest by the order's e-mail through the order payment link: the
  payment module would read another module's personal data to answer for its
  own.
