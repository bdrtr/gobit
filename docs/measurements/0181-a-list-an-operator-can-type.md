# A list an operator can type — measured 2026-09-25

The evidence behind [ADR 0181](../adr/0181-the-panel-edits-a-products-neighbors.md).

## 1. What the panel could reach

| Door | Before | What it offered for relations |
|---|---|---|
| read layer, `product` entity | the record carried `category_ids` and `tag_ids`, filled on request (ADR 0148) | nothing |
| read layer, provider filters | `handle` takes ONE string (`stringFilter`) | a lookup per line |
| `product.admin` surface | `UpdateProductBasics`, `ScheduleProduct`, `UnscheduleProduct` | nothing |
| `core/query.Provider` | `List` and `FetchByIDs(ids)` | an entity of its own would need an id a relation does not have |

The lists therefore went onto the product record, the way the memberships did,
and handle resolution went into the module, which reads all the handles in one
statement (`ListProductsByHandles`, `handle = ANY($1) AND deleted_at IS NULL`).

## 2. The cost of the record

`TestTheRelationsAreReadOnlyWhenAskedFor` pins it. A page of three products read
with `id, title` makes no relation read. The same page read with `up_sell_ids`
makes exactly one read, and does not read the memberships it did not ask for.
A whole-record read includes the three lists, since an empty selection is the
whole record.

The product page makes one more read for the related products, over the union
of the three lists and with four fields. It makes none when all three lists are
empty; `TestAProductWithoutRelatedProductsCostsNoRead` holds that.

## 3. All or none

ADR 0180's `SetProductRelations` became `SetRelationLists` with one kind in the
map. It checks every list in the kinds' fixed order, looks up every id at once,
and only then opens the transaction that replaces them.
`TestTheAdminSurfaceSavesEveryListOrNone` saves three lists and then sends three
bad saves: an unknown handle, the product's own handle, and an unknown kind.
Each bad save puts a valid list beside the bad one, and after each the stored
lists are the ones from before.

## 4. The panel meets the module

`TestThePanelEditsARealProductsNeighbors` (`internal/app`, Docker) opens a real
installation and builds the panel with `adminui.FromContainer`, which is what
the binary does. The identity ring is left out and the principal is put in by
hand. The test saves a draft's handle, a published product's handle and an id.
The module stored them in order. The product page shows them in order, with
the draft marked, and the form shows the handles back. A save naming a handle
nobody has answers 422 and leaves the lists alone. It passed on its first run.

## 5. What bites

Every command was green before each mutation was applied.

| Mutation | Result |
|---|---|
| a deleted product's handle resolves (SQL, generated) | integration red, **after the fixture was fixed** (below) |
| the relation fields are never filled | unit red, app integration red |
| the batch read orders by id instead of rank | app integration red |
| the admin surface cuts the lists without advancing | unit red |
| a prefixed reference is taken as a handle | unit red, **after re-aiming** (below) |
| the form's lines are not trimmed | panel unit red |
| the panel misspells a field | arch red |
| the panel's printed limit drifts from the module's | arch red |
| the "the storefront leaves it out" mark is dropped | panel unit red |
| a vanished product is shown as a server fault | panel unit red, **after the assertion was tightened** (below) |

**The deleted handle survived first.** The fixture deleted a product and gave
its handle to a successor. With the `deleted_at` condition gone, both rows came
back, and the map kept the last one, which was the successor. The fixture only
passed because of the row order. A second case now uses a handle that only a
deleted product carries. The refusal must name that handle as typed, and with
the mutation it names the deleted row's id one step later.

**The prefix mutation was aimed at the wrong line.** It changed the loop that
collects handles, which only added ids to the lookup. The loop that builds the
answer still read them as ids, so the mutant did the same thing as the
original. Aimed at the second loop, it goes red.

**The not-found branch looked redundant.** `unexpectedFailure` already maps a
NotFound to 404, so a test that checked only the status could not tell the
branch was there. What the branch adds is the sentence the operator reads and
the absence of a server-fault log line. The test now asserts the sentence.

## 6. Also fixed

`internal/adminui/catalog.go` carried `product\'s` inside a Go comment since
ADR 0179. A scripted edit had escaped the quote. The comment now reads
`product's`.
