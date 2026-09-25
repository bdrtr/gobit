# A launch at nine — measured 2026-09-25

The evidence behind [ADR 0177](../adr/0177-a-draft-can-be-scheduled.md).

## 1. Where "is this product visible" is decided

Every place reads the status and nothing else:

| Where | What it asks |
|---|---|
| `product/service/store.go`, the listing and the channel-scoped reads | `status = published` |
| `product/service/store.go`, the single product | `product.Status != StatusPublished` → 404 |
| `product/service/store.go`, the variant visibility check | the same |
| `product/service/taxonomy.go`, the category counts | `published` |
| `plugins/searchpg`, the index | the `status` field of `product.updated`, and a reindex filtered on it |

A moment judged at read time would have to be added to each, and the search
index would still not re-index at the moment — it learns only from events. A
status changed at the moment needs none of them to change.

## 2. The one status write

`grep` of the product queries finds one statement that sets `status`: the
PATCH's `UpdateProduct`, a COALESCE per field. It is the one that has to clear
a schedule, and it does, in the same statement:

```
publish_at = CASE WHEN COALESCE(sqlc.narg('status')::text, status) = 'draft'
                  THEN publish_at ELSE NULL END
```

The SET list reads the old row, so the resulting status is spelled out rather
than read from the column. The load rig's bulk `INSERT`s write no `publish_at`
and stay valid.

## 3. The storefront and the moment

`service.StoreProduct` embeds `models.Product`. The first draft shadowed the
new field in `StoreProduct` with `json:"-"`. The storefront describe test then
went red: the published schema carried `publish_at` while the encoded body did
not. `encoding/json` drops a field tagged `-` before it resolves names, so the
tag never hides the embedded field. The body only lacked it because the
sample's moment was nil.

The field is therefore `json:"-"` on the model, and the admin surface answers
with `adminProduct`, which adds it back. Putting the tag back on the model
reddens the storefront describe tests. The e2e asserts that a storefront body
never contains the string.

## 4. What bites

| Mutation | Result |
|---|---|
| the status update keeps the moment (SQL, regenerated) | integration red: publishing by hand trips the constraint |
| the choosing subquery without `FOR UPDATE SKIP LOCKED` | integration red in 5 s: the pass waited on a locked row |
| the pass publishes no event | unit red |
| the model writes `publish_at` | storefront describe tests red |
| the pass asks a day behind the clock | e2e red: the due draft stays unpublished |

The `SKIP LOCKED` case first HUNG for eleven minutes rather than failing. The
test failed correctly, but it left its lock-holding transaction open. Closing
the pool at the end of the run then waited on that connection for ever. The
transaction is now rolled back in `t.Cleanup`, and the same mutation fails in
fifteen seconds.

## 5. End to end

`TestAScheduledDraftGoesLiveWhenTheJobFindsItDue` (`internal/e2e`) creates a
draft over the admin API and schedules it an hour ahead. The admin answer shows
`publish_at`, and the storefront answers 404. The test moves the moment into
the past in SQL, which is how it makes time pass, and runs one pass of the job
definition the binary registers. The storefront then serves the product, with
no `publish_at` in the body. The admin record says `published`, with no moment.

## 6. The column list, forgotten the second way it can be

The storefront and admin listings run a hand-written statement whose column
list is named and read by position. Its godoc says a column appended to the
table is the one case the list must be edited for. It records that the list was
forgotten on 2026-09-09. It was forgotten again here. Every unit test was green.
The integration lane went red in 46 tests across the catalog, the GraphQL
reads, the paging and the channel filters, and the smoke lane's GraphQL query
answered `product_db_failed`.

The first read of that lane was wrong. The background wrapper echoed the exit
code into the log and itself exited 0, and the notification said 0. The log's
own line said 2. Every lane since has been read by its own exit status.

The existing pin for the list's order reads a published product, whose
`publish_at` is always NULL. Swapping `publish_at` and `deleted_at` in the list
left it green. A new pin reads a scheduled draft through the admin listing, and
it goes red under the same swap.

The migration test's version, written out on purpose so that an added migration
is noticed, went from 6 to 7. It now also checks that the new index appears and
goes away.
