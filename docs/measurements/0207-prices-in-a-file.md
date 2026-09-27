# Prices in a file — measured 2026-09-27

The evidence behind [ADR 0207](../adr/0207-an-import-writes-its-prices-through-pricing.md).

## 1. What the prices could be written through

| Part | State |
|---|---|
| pricing's `SetBasePrices` (interop) | replaces the whole set, deleting every price not named; no caller in the tree |
| pricing's `SetBasePriceAmount` (admin surface) | one currency, and `.admin` names are the panel's alone (`TestAdminSurfaceHasOneAudience`) |
| the write at one unit | ADR 0206's, unexported until this record |
| pricing's `CreateEmptyPriceSet` | no caller in the tree; its godoc said the product module called it on every new variant (D144) |
| the variant's link to its set | the product module's own: `firstLink` reads it and `SetVariantPriceSet` writes it |
| a module resolving another's surface | established: the product module resolves the file module's read-back lazily and optionally, and the auth module sends through the notification module's |

## 2. The full catalog with its prices, unchanged

A copy of the load database (52,004 products, 54,000 variants, 58,000 prices,
54,000 links from a variant to its set), served by the server built from this
change. Its export (6,790,899 bytes, 54,004 rows, 54,000 of them with a TRY
price) was sent back unchanged:

| Moment | Observed |
|---|---|
| the POST | 202 in 100 ms |
| while a run works | about 2,600 rows every 20 s, 130 a second |
| the end | 54,004 rows in 10 min 39 s; 0 created, 0 updated, 0 failed |
| afterwards | 58,000 prices, none created after the import began; 54,000 sets and 54,000 links |

ADR 0205's measurement of the same file, with the price columns read and not
applied, took 9 min 38 s at about 136 rows a second. Reading each variant's
link and price set costs about 0.3 ms a row.

## 3. Changed prices

The first 2,000 priced rows of the same export, each TRY price raised by 100
minor units, sent as a second import:

| Moment | Observed |
|---|---|
| the POST | 202 in 20 ms |
| the run | 2,000 rows updated in 31.5 s, about 63 a second |
| afterwards | 58,000 prices; 2,000 created after the import began, one in each of 2,000 sets, none of them on a list or a tier |

Five of the rows, the first three and the last two, were read back through the
link table: each variant's base price at one unit held the file's new amount.

The copy was dropped afterwards.

## 4. Mutations

| # | Mutation | Killed by |
|---|---|---|
| M1 | prices read and not written | the service's test, the end-to-end test |
| M2 | no set given to a variant without one | the service's test, the end-to-end test |
| M3 | a price on a row with no variant taken | the service's test |
| M4 | a price that is not a whole number skipped | the service's test |
| M5 | a price change not counted as an update | the service's test |
| M6 | a file with prices taken without pricing | the service's test |
| M7 | pricing's absence passed on as unavailable | the service's test |
| M8 | the pricing scope not asked | the API's test |
| M9 | price columns not seen when the file is sent | the API's test, the service's test |
| M10 | the module wiring no pricing | the end-to-end test |
| M11 | the product written before the row's prices are read | the service's test |

M6 and M9 first failed to compile, which is not a kill; both were written again
to compile and failed on assertions.
