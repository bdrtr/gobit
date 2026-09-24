# What a price was — measured 2026-09-24

The evidence behind [ADR 0167](../adr/0167-a-price-keeps-its-history.md).

The feature list's C1 row asked for price provenance: every price resolution
recording where it came from, a query endpoint, and the lowest price of the last
thirty days. Three read-only surveys of the tree at `2737eb2` came first — how
pricing stores and resolves a price, how a shopper sees one, and whether any
record of a past price exists anywhere — and this is what they found, in the
order the decision needed it.

## 1. What the tree kept of a price: nothing, on purpose

- `Repo.ReplacePrices` locks the set, runs `DeletePricesBySet` — a hard
  `DELETE` — and inserts the new rows with new ids. No price row is ever
  updated; there is no update query. [ADR 0047](../adr/0047-a-replaced-price-is-deleted.md)
  made the delete deliberate: the soft-deleted rows the older code left had no
  reader, no index and regenerated ids, and it rejected a price-change table
  "because nothing consumes it", while saying a shop that needs a timeline
  should build one on purpose.
- A price list's header — type, status, `starts_at`, `ends_at` — is updated in
  place by `UpdatePriceList`. Nothing sets a list to `expired`; a window simply
  stops admitting it.
- A rule can be added to or removed from one price at a time, outside
  `ReplacePrices`, and a rule is what takes a price out of the storefront.
- No event is published on a price change, and `audit_log` records who called
  which path and the status, never an amount.

So "what did this variant cost last month" was unanswerable by decision, and so
was the number a shop needs to announce a reduction.

## 2. The rule the reference comes from

The EU's Price Indication Directive, Article 6a (added by the Omnibus Directive
2019/2161): an announcement of a price reduction indicates the prior price, and
the prior price is the lowest price the trader applied during at least the
thirty days before the reduction was applied. A progressive reduction keeps the
reference of the first cut. Member states may shorten or adapt the period for
perishable goods and for goods on the market for less than thirty days; this
record implements the general rule and leaves those variants to a shop that
reads the timeline itself. Whether Turkey's price-label rules state the same
reference was NOT verified in this record, and nothing in the tree claims it.

## 3. How a shopper saw a price

The storefront's two surfaces — the product listing, through the pricing Query
provider, and `GET /store/v1/price-sets/{id}` — both showed the "listable"
prices: rule-less, with a usable list or none. An active sale showed as a
second amount beside the base price, told apart only by a non-null
`price_list_id`; the list's type was not in the record, `calculatedPriceDTO`
carried it on the admin surface alone, and the example storefront printed the
first price whatever it was. A search of every document for Omnibus, "lowest
price in the last 30 days", or the Turkish for "previous price" and "price
before the reduction" found nothing.

## 4. Why the input and not the answer

The price a shopper is charged is the ladder's answer over the live rows and
the lists' states, and it changes with no write: a sale window opens at its
`starts_at` whether anybody touches the catalog or not. A record of answers
taken at write time would miss exactly the moment a reduction is made of. What
is kept is the ladder's input after every write — a set's live prices with
their rules, a list's type, status, window and deletion — and the past answer is
`selectPrice`, unchanged, run at every moment the answer can change: a
snapshot of the set, a snapshot of one of its lists, a window's start, and the
first instant after a window's end, because `Usable` admits a list AT its
`ends_at`. `TestASaleWindowOpensAndClosesWithoutAWrite` holds the last two.

Keeping the rows ADR 0047 deletes was weighed and set aside: it would record
prices but not list headers, which change in place, so the window a sale price
competed in would still be lost.

## 5. Eight writers and one gate

The module writes the four tables the ladder reads through ten queries, found
by parsing the query files: `InsertPriceSet`, `SoftDeletePriceSet`,
`InsertPrice`, `DeletePricesBySet`, `SoftDeletePricesBySet`, `InsertPriceRule`,
`SoftDeletePriceRule`, `InsertPriceList`, `UpdatePriceList`,
`SoftDeletePriceList`. They are called from eight repository functions and one
helper (`insertPrices`, shared by the set's creation and replacement). Every
one now takes a snapshot in its own transaction; the rule and list writers,
which were single statements, gained one. `TestEveryPriceWriteLeavesAHistorySnapshot`
derives the query population from the SQL and requires every caller, or every
caller of a one-level helper, to take a snapshot; `TestEveryWriterLeavesASnapshot`
walks the eight writes on a real server.

One write takes none on purpose: a rule written to a DELETED price, which the
foreign key admits (`TestSilinmisFiyataKuralYazilabilirAmaUlasilamaz`) and which
changes nothing the ladder reads.

Triggers were weighed as the stronger guarantee — a direct SQL write could not
skip them — and set aside: the one procedural code in the tree's migrations is
the invoice retention guard, which refuses, and a trigger that WRITES would be
the first business logic in the schema. The gate holds the Go writers, and the
append-only gate holds the SQL.

## 6. Where the history starts

The migration records every live set, with its prices and rules, and every
list, once, stamped with the database's clock. The seed builds its JSON in SQL
and the repository reads it in Go; `TestTheSeedIsReadAsTheWriterWrites` rolls
the migration back over live data and forward again and compares the seed's
snapshot with the writer's, field for field.

Nothing before the seed is known. A timeline that reaches further back is
answered from `recorded_since` and says `covered: false`; a reduction running
when the history began has no stated start; and a reference whose thirty days
the history does not hold is absent. For thirty days after an upgrade, then, no
storefront reduction carries `lowest_prior_amount` unless its sale began after
the upgrade and the thirty days before that are also recorded.

## 7. One path for both storefront surfaces

`Service.storePrices` builds what both surfaces show: listable prices with the
list's type, the ladder's winner per currency at quantity one, and — for a
winner from a sale list — the history read for that set only, batched. The
reduction is the run of sale-list answers the timeline ends in; its start is
`reduced_since`, and the lowest answer in the thirty days before is
`lowest_prior_amount`. If the history's last answer is not the price charged
now — what a write without a snapshot would cause — nothing is announced
(`TestAHistoryThatDisagreesWithTheCatalogAnnouncesNothing`).

The price set endpoint's body had shared the admin one "because the fields were
the same". They are not any more, so the storefront has its own DTO, and the
body gate compares it with its encoded keys like every other.
`TestAReductionIsReadFromTheRealHistory` runs the whole decision on a real
server: a base price lowered and then raised before a sale, the sale window
opening with no write, and both surfaces announcing 9,000 — the lowest of the
thirty days — rather than the 11,000 the price was raised to five days before.

## 8. The mutations

Each applied alone, the named lane run with `-count=1`, the file restored and
its checksum compared.

| # | Mutation | What went red |
|---|---|---|
| H1 | `UpdatePriceList` takes no snapshot | `TestEveryPriceWriteLeavesAHistorySnapshot` |
| H2 | `DeletePriceRule` takes no snapshot | same |
| H3 | `ReplacePrices` takes none, while the helper's other caller still does | same |
| H4 | a query UPDATEs `price_set_history` | `TestThePriceHistoryIsAppendOnlyInSQL` |
| T1 | a window's end read as exclusive | `TestASaleWindowOpensAndClosesWithoutAWrite` |
| T2 | the reduction's run walked back one stretch only | `TestAReductionNamesTheLowestPriceOfTheDaysBeforeIt` |
| T3 | a reference announced without its thirty days covered | `TestAReferenceTheHistoryCannotReachIsNotAnnounced` |
| T4 | a deleted list's price keeps competing | `TestAListWriteChangesThePriceAtTheWrite` |
| T5 | the first snapshot stands instead of the latest before the moment | `TestAReplacedPriceIsStillThePriceThatApplied`, and the reduction test |
| T6 | a reference announced when the history's last price is not the live one | `TestAHistoryThatDisagreesWithTheCatalogAnnouncesNothing`, written after the mutation survived |
| S1 | the migration's seed writes `minimum_quantity` where the reader reads `min_quantity` | `TestTheSeedIsReadAsTheWriterWrites` |

## 9. What this record does not close

- **The other half of C1: a cart line's provenance.** The cart receives only an
  amount from pricing; which list or rule produced a line's unit price is not
  recorded on the line.
- **Prices with a context.** The timeline and the reduction are at quantity one
  with no rule context, which is the storefront's price. A customer group's or a
  region's price has a history in the snapshots and no reduction announced.
- **The history is read whole.** A sale price's reduction reads every snapshot
  of its set and its lists; a set edited thousands of times makes its storefront
  read heavier, and nothing bounds it yet.
- **The rule's variants.** Perishable goods, goods on the market for less than
  thirty days, and member-state periods other than thirty days are the shop's
  to apply, from the timeline.
- **The example storefront** still prints the first price; it does not yet
  strike through a reduced one.
