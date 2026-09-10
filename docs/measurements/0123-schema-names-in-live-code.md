# Schema names in live code — measured 2026-09-10

Serves [ADR 0123](../adr/0123-a-schema-name-in-live-code-resolves.md).

## The population, and why it is a closed vocabulary

Walking every `*.up.sql` under the production trees in migration order:

| | |
|---|---|
| named objects the migrations have ever defined | 459 |
| still in the schema | 454 |
| dropped and not put back | 5 |

The five: `event_outbox_pending_idx`, `fulfillments_alive_idx`,
`inventory_movements_sale_names_its_reservation`, `invoices_buyer_email_idx`,
`order_exchanges_completed_owes_nothing`.

The audit's vocabulary is those 459 names and nothing else. A snake_case word in
a comment can be a column, a JSON key, a struct field or an English phrase with
underscores, so asking "does this look like a constraint" would be an exemption
list with a check attached — the shape gap D16 records. Asking "did this tree
ever define an object by this name, and does it still" needs no list.

## What live code says about the five

One mention, and it is this audit's own account of why it exists. Every other
live sentence naming a dropped object was corrected the same day, by hand,
before the audit existed — which is the point: it was found by sweeping for
something else.

Dated records are outside the audit and one of them names a dropped constraint
correctly: the changelog's entry for the exchange's completion describes the
world of 2026-09-06, when that CHECK was the rule.

## The ordering is the computation, and the naive version was measured wrong

The first version collected every `ADD CONSTRAINT` into one set and every `DROP
CONSTRAINT` into another and subtracted. It reported `fulfillments_returned_stamp`
as dead, named in three live files.

It is not dead. `000004_a_parcel_can_come_back.up.sql` drops it on line 77 and
adds it back on line 78, which is how a CHECK is widened in this repository. The
sets have no order, so the drop won regardless of which line came first.

The audit therefore walks the files in path order and the lines in file order,
and the LAST thing done to a name decides. The mutation that returns the naive
behaviour is in the test file's history: it re-reports those three files.

## Down-migrations are not read

A down-migration describes the schema of a rollback. A name it restores is not a
name the tree has, and reading them would resurrect all five.
