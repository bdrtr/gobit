# ADR 0024 — An invoice number comes from a ROW, not a sequence

- **Status:** Accepted
- **Date:** 2026-09-05
- **Phase:** after the roadmap

## Context

The measured gap inventory named invoicing as the one item on the must-have
checklist that no part of the framework touched: every "fatura" in the codebase
was a billing ADDRESS, and for a shop selling in Turkey an invoice is a legal
requirement rather than a feature.

A framework cannot file an invoice on a merchant's behalf — that needs the
merchant's own certificate and a contract with an integrator — so what it owes
is the document, its numbering, and a place for the transmission to plug in.
Of those three, the numbering is the one with a decision in it.

The repository already numbers something for a human to read. `order_init`
argues its case carefully: `display_id` comes from an IDENTITY sequence, because
a sequence advances atomically, two concurrent inserts cannot take the same
value, and *"there is no COMMON row to lock, both of them open a NEW row"*.

That argument is right about order numbers and wrong about invoice numbers, and
the difference is legal rather than technical.

## Decision

**An invoice number is allocated by an UPDATE on a series ROW, inside the same
transaction that writes the document.**

Three properties follow, and each of them is the answer to a question a sequence
answers differently:

1. **Gap-free.** A sequence advances outside the transaction, so a transaction
   that rolls back burns its number and leaves a hole. A row's counter is part
   of the transaction: a failed issue takes the increment back with it.

2. **Serialized per series.** The UPDATE takes the row lock itself and holds it
   until the transaction ends, and it re-reads the row after acquiring it — so
   `last_number + 1` is computed from what the other transaction committed, not
   from a stale read. **No `SELECT ... FOR UPDATE` is taken alongside it.** A
   lock before an UPDATE that is about to take the same lock is protection that
   looks like protection and adds none.

3. **Opened and advanced in one statement.** `INSERT ... ON CONFLICT (prefix,
   year) DO UPDATE SET last_number = last_number + 1 RETURNING *` opens the
   series if the year is new and advances it if it is not.

## Why gap-freeness is worth the contention

A tax authority reading a series that jumps from 41 to 43 sees a document that
was issued and then made to disappear, and the shop has to prove otherwise. For
an order number a hole is harmless — nobody audits the gaps in "your order
number 1042".

The cost is that documents in the same series serialize on one row. That is
bounded: the contention is per series and per year, an invoice is issued once
per sale rather than per request, and the alternative is not "faster invoices"
but "invoices the shop cannot defend".

## Why the third property is not an implementation detail

The obvious arrangement — look for the series, create it if missing, advance it
— cannot recover from its own race. Two callers issuing the first document of a
year both find nothing and both insert; one gets a unique violation, and in
PostgreSQL an error inside a transaction POISONS it (SQLSTATE 25P02): every
statement after it fails too, so the "read the winner's row instead" fallback
has nothing left to run in.

That was written, and the module's concurrency test found it. `ON CONFLICT`
never raises, so there is no error to recover from.

## Consequences

- **There is no draft status.** A draft would need a number to be a draft OF
  anything, and a number given to a draft that is then abandoned is exactly the
  hole this decision exists to prevent. A document is either issued or it does
  not exist.
- **A canceled document keeps its number and stays in the table.** Deleting it
  would put the hole in from the other end.
- **An issued document is immutable.** There is no update path for its amounts,
  its parties or its lines; only the status and the transmission fields move. A
  mistake is corrected with a cancellation and a new document, which is also how
  the law treats it.
- **The series prefix is validated before the number is taken.** A document the
  regime would refuse for its prefix would already have spent its number, and
  the shop would be left with a hole it cannot fill.
- **The invoice module knows no other module.** It is handed a finished document
  and checks that it adds up; assembling one from an order belongs to a workflow
  (ADR 0001/0006), and transmitting one belongs to a plugin.

## What was verified

- A failed issue gives its number back: the next document takes the number the
  failed one was going to have. Committing the allocation outside the caller's
  transaction makes this test fail with the burned number in the message.
- Twenty concurrent issues against one series produce 1..20 with nothing missing
  and nothing repeated. Removing `ON CONFLICT` makes this test fail.

Neither claim can be shown with a fake repository: one needs a real rollback,
the other a real row lock.
