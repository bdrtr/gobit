# Soft delete in the order and payment modules — measured 2026-09-08

Evidence for [ADR 0054](../adr/0054-an-order-and-a-payment-are-never-deleted.md).
Everything below was taken on 2026-09-08, against commit `3c912f3` and the
repository's own PostgreSQL container (`PostgreSQL 16.14 on x86_64-pc-linux-musl`).

## 1. What the ten columns cost

Ten `deleted_at` columns: six in `order` (`orders`, `order_line_items`,
`order_returns`, `order_return_items`, `order_claims`, `order_exchanges`) and
four in `payment` (`payment_collections`, `payment_sessions`, `payments`,
`refunds`). `order_summaries`, `order_addresses` and `payment_manual_sessions`
never had one.

| | order | payment | total |
|---|---:|---:|---:|
| sqlc statements | 52 | 31 | 83 |
| …carrying `deleted_at IS NULL` | 34 | 22 | **56** |
| indexes created by the migrations | 17 | 9 | 26 |
| …partial on `deleted_at` | 15 | 8 | **23** |
| …of those, UNIQUE | 2 | 2 | **4** |

Nothing wrote the column. The only occurrence of `deleted_at` outside a
declaration or an `IS NULL` predicate, across both modules' migrations and query
files, was `o.deleted_at` in the SELECT list of `ListOrdersForErasure`. Six
reads deliberately left the predicate off their own table — `ListOrdersForErasure`
and the five `ListFor…Disclosure` statements — because a data subject is owed
what the database HOLDS rather than what the shop can see (ADR 0034).

Six statements in the two modules' integration tests stamped the column by hand,
so that a read could be proven to hide a row. Those are the only writes that
have ever existed.

## 2. The four indexes written as "unique among LIVING rows"

`fulfillment`'s migration `000003_fulfillments_are_never_deleted.up.sql` removed
a column of this shape and distinguished itself from these ten with:

> The CONCLUSION differs, and the reason is the index below: this table's
> uniqueness rule is written as "unique among LIVING rows" … **D9's ten carry no
> such rule.**

That sentence is false. Four of the ten carry exactly that rule:

| index | table | predicate |
|---|---|---|
| `orders_idempotency_key_uniq` | `orders` | `idempotency_key IS NOT NULL AND deleted_at IS NULL` |
| `order_return_items_line_uniq` | `order_return_items` | `deleted_at IS NULL` |
| `payment_sessions_provider_idempotency_uniq` | `payment_sessions` | `deleted_at IS NULL` |
| `payments_session_uniq` | `payments` | `deleted_at IS NULL` |

### The hole, reproduced

```sql
CREATE TABLE probe (id TEXT PRIMARY KEY, idempotency_key TEXT, deleted_at TIMESTAMPTZ);
CREATE UNIQUE INDEX probe_idempotency_uniq ON probe (idempotency_key)
  WHERE idempotency_key IS NOT NULL AND deleted_at IS NULL;

INSERT INTO probe (id, idempotency_key) VALUES ('a','K1');   -- INSERT 0 1
UPDATE probe SET deleted_at = now() WHERE id = 'a';          -- UPDATE 1
INSERT INTO probe (id, idempotency_key) VALUES ('b','K1');   -- INSERT 0 1   <-- second live row, same key
```

One hand-written UPDATE and the key is reusable. On `payments_session_uniq` and
`payment_sessions_provider_idempotency_uniq` that is the guarantee standing
between a retried saga step and a second charge.

The unconditional index the migrations build refuses the state instead:

```
CREATE UNIQUE INDEX probe_full_uniq ON probe (idempotency_key);
ERROR:  could not create unique index "probe_full_uniq"
DETAIL:  Key (idempotency_key)=(K1) is duplicated.
```

That is also how an installation that HAS stamped the column finds out: the
migration fails loudly rather than dropping a guarantee quietly.

## 3. DROP COLUMN takes the index with it, silently

Same cluster, fresh probe with a UNIQUE partial index and an ordinary one:

```
before:  probe_pkey, probe_alive_idx, probe_idempotency_uniq
INSERT INTO probe (id, idempotency_key) VALUES ('b','K1');
ERROR:  duplicate key value violates unique constraint "probe_idempotency_uniq"

ALTER TABLE probe DROP COLUMN deleted_at;
ALTER TABLE                       <-- no NOTICE, no WARNING

after:   probe_pkey
INSERT INTO probe (id, idempotency_key) VALUES ('b','K1');
INSERT 0 1                        <-- accepted
```

Both indexes whose PREDICATE named the column are gone; the primary key, which
did not, survived. This is why both migrations rebuild every partial index they
drop, and it reproduces on this cluster the finding `fulfillment` 000003 and
`region` 000003 recorded on their own.

## 4. The migrations, applied and rolled back

`d9probe` and `d9base` were built from the migration files with `psql`.

* `d9probe`: order `000001`→`000010`, payment `000001`→`000003`. Result: **no
  table in either module declares `deleted_at`**, and 40 index definitions
  stand, including all 23 rebuilt ones.
* `d9base`: order `000001`→`000009`, payment `000001`→`000002` — the schema as
  it was before this change.
* `d9probe` then had `000010.down.sql` and `000003.down.sql` applied. Its 40
  index definitions and its full `information_schema.columns` listing are
  **byte-identical** to `d9base`.

The round trip is therefore closed: up removes exactly what down restores.

## 5. `authorized_at` — the sibling in section G

**The column does not exist.** `grep -rn "authorized_at\|AuthorizedAt"` over the
whole tree returned two hits before this change, both in `docs/gaps.md`'s own
row about it. No migration, no query, no model, no DTO.

**The moment is already readable while it matters.** Every write to a session's
status goes through `UpdatePaymentSessionState`, and there are exactly four call
sites:

| call site | status written |
|---|---|
| `service/session.go` (authorize, provider said authorized) | `authorized` |
| `service/session.go` (authorize, provider declined) | `failed` |
| `service/session.go` (cancel) | `canceled` |
| `service/capture.go` | `captured` |

None writes `authorized` onto a session that is already `authorized`:
`AuthorizeAction()` returns `ActionNoop` for that status and the service returns
from the transaction before the provider is called and before anything is
written. So for a session whose status IS `authorized`, `updated_at` is the
moment the hold was taken — which is what `ListSessionsForReconciliation` and
`payment_sessions_reconcile_idx` have always relied on ("has looked that way for
longer than the settling window").

**What is genuinely lost is the moment after capture**, and nothing asks for it:
the money-event surface (`PaymentMomentsByCollectionIDs` → `models.PaymentMoments`
→ order's `payment_view.go` → the admin order body) is read by one route and one
e2e assertion, neither of which mentions an authorization.

The other half of the section G row — `refunded_at` — was already closed and is
not reopened here: `refunds` carries no UPDATE statement anywhere in the module,
so `created_at` IS the refund moment.
