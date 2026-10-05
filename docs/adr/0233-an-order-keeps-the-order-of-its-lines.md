# ADR 0233 — An order keeps the order of its lines

**Summary:** Cart and order lines carry a sequence the database assigns as it
takes each row, and every read of a cart's or an order's lines orders by their
moment and then that sequence, so lines written together come back as written.

- **Status:** Accepted
- **Date:** 2026-09-29
- **Amended by:** [0393](0393-an-add-on-is-written-under-its-line.md): an add-on is written right after its own line

Measurement: [measurements/0233](../measurements/0233-twelve-lines-in-a-row.md)

## Context

An order's lines are written in one transaction and a cart's add-ons with their
line (ADR 0229), so each group shares a `created_at`, and the ids the reads
broke the tie with end in random bits. Both modules said their lines were read
in creation order, and twelve lines written in order came back shuffled: the
order read, its invoice and the dossier printed an engraving before its ring as
often as after it (D157).

## Decision

`order_line_items` and `cart_line_items` gain `seq`, an identity column the
database fills in the order it takes the rows, and the reads of an order's or a
cart's lines order by `created_at` and then `seq`. The write loops insert in the
order they were given, so the reads return that order.

## Consequences

A line and its add-ons come back parent first, as the checkout writes them, and
an order of twelve lines prints them in the order the cart held them. The column
is the database's: no write names it, and a line inserted by any future path is
ordered by the same rule. The rows already stored are numbered in the order the
table held them when the migration ran, which is no worse than the order they
were read in before. The reads that order by other keys, the newest sales first
or a batch by id, are unchanged.

## Rejected

- **A position written by the order's write loop.** It covers the order and not
  the cart's add-ons or merges, and every new write path would have to remember
  it.
- **Ids monotonic within a millisecond.** Every module's generator repeats the
  same code by design (ADR 0001), and the order of rows would still rest on the
  clock of whichever process wrote them.
