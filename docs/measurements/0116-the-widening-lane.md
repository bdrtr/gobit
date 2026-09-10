# The widening lane — measured 2026-09-10

Evidence for [ADR 0116](../adr/0116-a-cardinality-can-be-widened.md). Two
questions: what the tree actually did before the change, and whether the gates
that now claim otherwise can be made to fail.

## What the tree did before

`core/link` held a durable ledger row per definition and compared an incoming
declaration against it for equality (`storedDefinition.matches`). Declaring
`order_payment` one step wider therefore did not loosen a constraint — it
stopped the process. Reproduced against a real cluster before any code changed:

```
link_definition_conflict: link "order_payment" is declared differently in the
link_definitions table: stored (order.order_id -> payment.payment_collection_id,
one_to_one), incoming (order.order_id -> payment.payment_collection_id,
one_to_many)
```

The schema half is independent of the ledger half and fails differently. Every
statement in `linkTable.ddl` was `IF NOT EXISTS`, so a looser declaration
created nothing and dropped nothing: `link_order_payment_from_uniq`, built when
the link was one-to-one, survived. With the ledger check removed but the drop
absent, the second collection is refused by an index the definition no longer
declares, and the message names a cardinality that permits the row:

```
link_cardinality_violation: link widen_cardinality has cardinality one_to_many:
record "order_1" is already bound to another target
```

That sentence is the reason `verifySchema` now asks the second question. An
operator reading it has been told the constraint permits what the database just
refused, and nothing in the message points at the leftover index.

## Mutation proof

Each mutation was applied to the production code alone, run with `-count=1`, and
reverted. A gate that survives its mutation is not a gate.

| # | Mutation | Gate that went red | What it proves |
|---|---|---|---|
| A | `ddl` stops emitting the `DROP INDEX` statements | `TestALinkCanBeWidened` — Define fails with `the link_widen_cardinality_from_uniq index ... outlived the one_to_many cardinality it enforced` | The schema check catches a widening the DDL did not finish, at declaration time rather than at Create time |
| B | A, plus the obsolete-index check removed from `verifySchema` | `TestALinkCanBeWidened` — Create refuses `order_1` under `one_to_many` | The drop is load-bearing: without it the widening is a promise the database does not keep |
| C | `Cardinality.widerThan` returns `c != other` | `TestDefineRejectsChangedDefinition` (integration), `TestWiderThanOrdersTheCardinalities` and `TestStoredDefinitionChange/a_narrowing` (unit) | The direction is real. A relation that accepted any change would apply a narrowing blind |
| D | The `widenDefinitionSQL` exec is dropped from `declare` | `TestALinkCanBeWidened` — ledger reads `one_to_one`, expected `one_to_many` | The ledger moves with the schema; otherwise the next startup declares against a cardinality that is no longer enforced |

| E | A, measured against a ledger row that was DELETED rather than changed | `TestASchemaConvergesOnTheDeclarationWhenTheLedgerRowIsGone` — Define fails on the leftover `_from_uniq` | The schema converges on the DECLARATION, not on what the ledger was able to say |

Mutation E covers a path a peer session measured and this record would otherwise
have missed. A stored definition can be abandoned instead of changed: delete the
row, and the next startup's upsert INSERTs a fresh one carrying the new
cardinality, `RETURNING` hands back what was just written, and the comparison
compares the declaration against itself. Nothing in the ledger can object, and
every startup after that looks consistent while the old index still enforces the
old constraint. It is the reason the drops are unconditional rather than
reserved for the widening branch — a schema that converges only when the ledger
noticed a change is a schema that trusts the ledger to have noticed.

Mutation B is the one worth keeping in mind: A and B differ only in whether the
verification is present, and they fail at different moments with different
messages. That is the whole argument for the check — the same defect either
stops a deployment or reaches an operator as a contradiction.

## What a widening spends

Read independently by a peer session and confirmed in the code. Under
`OneToOne` the DDL writes two unique indexes; under `OneToMany` only `to_uniq`.
So the widening drops `from_uniq`, and `from_uniq` is the ONLY structural bar to
two writers binding two targets to the same left-side record: the flows read the
link and then write it, with no lock between the two, and `core/link` takes its
advisory lock around `Define` alone.

`internal/workflows/checkout/authorize_payment.go` is where the consequence is
concrete. It links the order to the collection BEFORE authorizing, and its godoc
gives the reason — "nothing has been held on the customer's card yet". Under
`OneToOne` a second concurrent binding therefore loses on the cardinality
constraint with no money moved. Under a widened link both succeed, each with its
own `collection_id`, and both can be driven through payment's published
`payment-sessions → authorize → capture` endpoints.

This is not an argument against the widening — the cost is what `OneToMany`
means — but it is the property a link gives up by crossing this line, and it is
worth knowing before crossing it rather than after. `order_payment` is not
widened by ADR 0116; the record that widens it owns this paragraph.

## The one-way door has two locks

The record says a release that widens a link cannot be rolled back through this
path, and names the first lock: the older binary declares the narrower
cardinality and `change` refuses it. That lock is human-defeatable — correct the
ledger row by hand and the declaration passes.

The second is not, and it follows from the decision's own backbone sentence read
backwards. Every pair a narrower cardinality admits is admitted by a wider one;
the converse fails, so rows written AFTER the widening may violate the narrower
constraint. Recreating `from_uniq` over them raises `23505`, inside the
declaration transaction, and the only way through is to delete rows — which is
to say, to throw away the records the widening existed to allow.

Noted by an independent reading of the record rather than measured here; it
sharpens the Consequences line rather than changing it.

## What was not measured

Index drop time on a large link table. `DROP INDEX` takes an ACCESS EXCLUSIVE
lock on the table for the duration, and this runs inside the declaration
transaction at startup, behind the advisory lock every declaration already
takes. On the tables gobit ships that is a table nobody is reading yet, because
the process has not begun serving. An embedder with a link table large enough
for the drop to matter would notice it as startup latency rather than as a
stall on a live path; `DROP INDEX CONCURRENTLY` cannot run inside a
transaction, so buying that back means giving up the atomicity of the
declaration, which is a different decision from this one.
