# Fifty thousand rows — measured 2026-09-27

The evidence behind [ADR 0204](../adr/0204-the-catalog-leaves-as-csv.md).

## 1. What B2.6 would need, and what exists

| Part | State |
|---|---|
| a job with a payload, and its status over HTTP | absent: `internal/core/job` runs scheduled jobs whose `Run` takes no payload, and the history is read only by the `gobit jobs` CLI |
| a CSV upload | refused: the file module detects `text/plain` from the bytes, and the configuration refuses any `text/*` type |
| a job reading an uploaded file back | absent: `file.interop` answers metadata only |
| a batch or bulk endpoint | absent anywhere |
| CSV or an export | absent anywhere (`encoding/csv` was imported nowhere) |
| paging the catalog | `ListProducts` pages by cursor and loads relations for a page in bulk queries (`WithRelations`) |
| a variant's prices | pricing's read-layer record, already expanded for the storefront, and read by ADR 0041's filter |

## 2. The export against the load database

The `gobit_load` database (memory `gobit-yuk-olcumu`): 52,004 products, 54,000
variants, 58,000 prices, one region selling in TRY. A copy of it was migrated
by the server built from this change, its user table emptied so the first
administrator could be created, and the export downloaded three times with
`curl` on the same machine:

| Run | Status | Bytes | First byte | Total |
|---|---|---|---|---|
| 1 | 200 | 6,790,899 | 12 ms | 1.45 s |
| 2 | 200 | 6,790,899 | 4 ms | 1.39 s |
| 3 | 200 | 6,790,899 | 3 ms | 1.39 s |

An earlier run, before the handler stopped keeping the response writer in a
struct for the error-path gate, took 1.24–1.25 s with the same bytes; the
table is the final code's.

The file read back with Python's `csv`: 54,004 rows for 52,004 products, 54,000
of them variants and 4 products with none. 54,000 rows carry a TRY price, and
the copy held exactly 54,000 base prices in TRY at one unit; the other 4,000
prices are the ones the definition leaves out. The copy was dropped afterwards.

The first byte arriving in milliseconds is the streaming: a buffered response
would have sent it after the whole file was built.

## 3. Mutations

| # | Mutation | Killed by |
|---|---|---|
| E1 | a product with no variant left out | the service's test |
| E2 | any price taken as the base | the service's test |
| E3 | a quantity tier taken as the price at one unit | the service's test |
| E4 | formulas not escaped | the service's test, e2e |
| E5 | a currency repeated in the header | the service's test |
| E6 | the first page only | the service's test |
| E7 | the page hook never called | the service's test |
| E8 | a variant's options keyed by value | the service's test |
| E9 | pricing's read scope not required | the API's test |
| E10 | a midway failure ending the response quietly | the API's test |
| E11 | not served as CSV | the API's test, e2e |
| E12 | an unknown status accepted | the API's test |

The test's fixture lists the base price last. Listed first, taking the first
price in a currency would choose it anyway and E2 could not fail; the order was
changed before the mutations ran.
