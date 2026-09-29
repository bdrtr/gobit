# A crushed box sent again — measured 2026-09-29

The evidence behind [ADR 0238](../adr/0238-a-bundle-is-replaced-from-its-parts.md),
D160 and D161.

## 1. What there was

| Part | State before |
|---|---|
| a replacement item | one promise, `order_replacement_items.reservation_id` |
| `ReplacementDetailJSON` | per line: the line's variant, its quantity, its promise |
| `holdStock` (returns flow) | per line: find the variant's inventory item, reserve the line's quantity, write the promise on the line |
| a bundle line | the variant has no inventory item (ADR 0234), so the dispatch answered `returns_workflow_no_inventory_item` |
| `MarkReplacementDispatched` | every item names a promise |
| `WithdrawReplacement` | releases each item's promise (ADR 0237) |
| the order line | keeps `components`, one unit's parts (ADR 0235) |

## 2. The shape

| Piece | Now |
|---|---|
| order migration 000034 | `order_replacement_item_parts (order_replacement_item_id, variant_id, quantity, rank, reservation_id)`; key (item, variant); rank unique per item; quantity 1–100, the order line's bound; blank variant or promise refused |
| creating a replacement | an item naming a line copies the line's `components` as its parts, in their order |
| `ListReplacementItems` | one more query per replacement, the parts of all its items |
| `RecordReplacementReservation` | takes the variant: a part of the item, or the variant the item sends (the line's, joined through `LineItemsByIDs`, or the item's own); anything else is `order_replacement_line_unknown` |
| `ReplacementItem.Held` | each part's promise, or the item's own |
| the flow | one promise per part, `line quantity × part units`, from the part's own item; the reservation's reference stays the order line |
| `sent_units` | the units taken out of the count, so the parts' units |
| the admin read | `items[].parts[]`: `variant_id`, `quantity`, `reservation_id` when held |

An item that names a bundle VARIANT (ADR 0145) carries no parts: the flow
reads no catalog, and the variant has no item, so it is refused as before.

## 3. The tests

| Test | Holds |
|---|---|
| `TestAReplacedBoxSendsThePartsItWasSoldWith` (order service) | the box item keeps the line's parts in order, the plain item none; the flow's document carries them |
| `TestEachPartOfABoxIsHeldUnderItsOwnPromise` | retry answered, a second promise refused, the box's own variant refused, the item holds nothing |
| `TestABoxIsNotRecordedSentUntilEveryPartIsHeld` | one soap with no promise refuses the record |
| `TestALineIsHeldOnlyForWhatItSends` | a promise for another variant is refused on both shapes of item |
| `TestAReplacedBoxKeepsItsPartsOnTheRealSchema` (order, Postgres) | rows, ranks and promises on the real table; the item's column stays NULL |
| `TestAReplacementPartIsHeldToAShape` | three CHECKs and the rank key, in raw SQL |
| `TestAReplacedBoxIsSentFromItsParts` (returns flow) | 2 towels and 4 soaps reserved for two boxes, promises written per part, confirmed after the parcel, 7 units sent |
| `TestAPartAlreadyHeldIsNotHeldAgain` | the retry confirms the earlier towel promise, reserves only the soaps |
| `TestAPartNoWarehouseStocksStopsTheBox` | refused on the part with no item, before any parcel |
| `TestAWithdrawnBoxGivesBackEveryPart` | every part's promise and the plain line's are released |
| `TestAGiftBoxIsReplacedFromItsParts` (end to end) | below |

The end-to-end scenario: two boxes of a towel and two soaps are sold (towels
10 → 8, soaps 10 → 6). The soap shelf is set to 3 and both boxes are asked for
again: the dispatch holds two towels and is refused on four soaps with
`returns_workflow_stock_not_held`; the admin read shows the towel's promise on
its part and none on the soap's. Withdrawing gives the towels back (sellable
8). The shelf goes back to 6, the catalog's box is remade of three soaps, and
one box is asked for again: `sent_units` is 3, towels 8 → 7, soaps 6 → 4.

## 4. D161 — rows written together

`TestAWithdrawnReplacementGivesBackTheUnitsItHeld` (ADR 0237) failed once on
this tree with the stocked line's unit NOT held (sellable 8, expected 7). The
same test on c21fa6e, run 8 times: 2 failures, the same assertion. A
replacement's lines are written in one transaction, so they share `now()`, and
were read `ORDER BY created_at, id`; the ids are 48 bits of milliseconds and 80
random bits, so two lines written in the same millisecond come back in either
order, and the dispatch reserves in the order it reads. The CI run of c21fa6e
passed.

The population: every `ORDER BY` in `*.sql` on `created_at` with an id tie,
18 on the first list and the rest by a second search, audited read-only for
(a) who fills `created_at`, (b) whether one transaction writes several rows the
read returns together, (c) whether anything claims or relies on their order.

| Read | (b) several rows in one transaction | (c) order relied on | Verdict |
|---|---|---|---|
| `ListOrderReturnItems` | the return's line loop | "in the order they were written" | fixed |
| `ListOrderReplacementItems` | the replacement's line loop | the dispatch reserves in this order | fixed |
| `ListOrderLineCancellations` | a line and its add-ons (ADR 0230) | "oldest first", the timeline's stable sort | fixed |
| `ListOrderShippingMethods` | the order's delivery loop | "in the order the cart held them" | fixed |
| `ListCartLineItemNotesForDisclosure` | a line and its add-ons (ADR 0229) | the dossier; the column ADR 0233 added was not used | fixed |
| `order_addresses` | yes, one row per type | the read splits by type first | fine |
| credit lines, delivery changes, claim evidence, cart shipping methods, inventory levels, the payment journals, imports, the review queue, the delivery-change and disclosure reads | one row per transaction | — | fine |

Aside, read in the code and not reproduced, and not fixed here: `now()` is the
transaction's start and `ChangeDelivery` takes the order's lock after it, so a
change that waited on the lock would be stamped before the change it waited
for, and `CurrentDeliveries` applies them in `created_at` order, the later
decision first.

| Test | Holds |
|---|---|
| `TestAReturnAndAReplacementKeepTheOrderOfTheirLines` (order, Postgres) | twelve lines named in reverse come back as named, on both |
| `TestAnOrdersDeliveriesAndAWriteOffsAddOnsKeepTheirOrder` | five deliveries as the cart held them; a ring's write-off, then its five add-ons' |
| `TestADisclosureKeepsTheOrderOfACartsLines` (cart, Postgres) | a ring and five add-ons with words, in the dossier as written |

## 5. D160 — the index's status column

A record's header names the record that amended or superseded it; the index row
said `current` for five of them:

| Record | Header | Row before |
|---|---|---|
| 0022 | Superseded by 0121 | current |
| 0057 | Amended by 0125 | current |
| 0085 | Superseded by 0091, the concurrency half | current |
| 0174 | Amended by 0175 | current |
| 0234 | Amended by 0235 | current |

0205's header also links 0206, as a reason rather than an amendment; the gate
takes the first link of a line, which is 0207, the amender.

## 6. Mutations

| # | Mutation | Killed by |
|---|---|---|
| M1 | no parts copied from the line | the order service tests |
| M2 | the parts of any bundle line copied | `TestAReplacedBoxSendsThePartsItWasSoldWith` |
| M3 | the parts not written | the Postgres tests |
| M4 | the parts not read back | `TestAReplacedBoxKeepsItsPartsOnTheRealSchema` |
| M5 | a second promise on a part accepted | `TestEachPartOfABoxIsHeldUnderItsOwnPromise` |
| M6 | a variant the box does not hold accepted | the same |
| M7 | a box held as one line | the order service tests |
| M8 | a plain line held for any variant | `TestALineIsHeldOnlyForWhatItSends` |
| M9 | a line-shaped item compared with its empty variant | the same, and three dispatch tests |
| M10 | a box counted held by its own promise | `TestABoxIsNotRecordedSentUntilEveryPartIsHeld` |
| M11 | a box counted held when one part is | the same |
| M12 | the flow's document without parts | `TestAReplacedBoxSendsThePartsItWasSoldWith` |
| M13 | the admin read without parts | first only the end-to-end test; now `TestAdminReplacementReadCarriesABoxsParts` |
| R1 | a part reserved once per box, not times its units | `TestAReplacedBoxIsSentFromItsParts` |
| R2 | a box held as its own variant | the flow's box tests |
| R3 | the promise recorded for the line's variant | `TestAReplacedBoxIsSentFromItsParts` |
| R4 | a part's promise not released on withdrawal | `TestAWithdrawnBoxGivesBackEveryPart` |
| R5 | sent units counted in boxes | `TestAReplacedBoxIsSentFromItsParts` |
| R6 | a held part reserved again | `TestAPartAlreadyHeldIsNotHeldAgain` |
| S1 | replacement lines by id | `TestAReturnAndAReplacementKeepTheOrderOfTheirLines` |
| S2 | return lines by id | the same |
| S3 | deliveries by id | `TestAnOrdersDeliveriesAndAWriteOffsAddOnsKeepTheirOrder` |
| S4 | write-offs by id | the same |
| S5 | the cart's dossier by id | `TestADisclosureKeepsTheOrderOfACartsLines` |
| G1 | 0234's row back to `current` | `TestTheADRIndexNamesEveryAmendment` |
| G2 | 0022's row back to `current` | the same |

Twenty-six mutants, all killed; M13 survived the API package on the first run
and was killed only end to end, so the API test was added and M13 run again.
