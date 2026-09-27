# A file worked through — measured 2026-09-27

The evidence behind [ADR 0205](../adr/0205-a-catalog-import-is-a-record-a-job-works-through.md).

## 1. What an import could stand on

| Part | State |
|---|---|
| a job with a payload | none: `job.Definition.Run` takes no payload, and jobs run on a schedule |
| a job's status over HTTP | none: the history is read by the `gobit jobs` CLI |
| the file module | takes images by default (`FILE_ALLOWED_TYPES`), serves at a public URL, and `file.interop` returns an upload's record, not its bytes |
| a request's body | JSON is capped at 1 MiB; `Idempotency-Key` buffers a body up to 1 MiB and refuses a larger one with 422 `body_too_large`, and skips multipart |
| a job reading a module's records | the scheduled publisher calls the product service once a minute |
| one transaction around several service writes | not available: every write method opens its own; the only helper taking a transaction is `createVariantTx` |

## 2. The full catalog, unchanged

A copy of the load database (52,004 products, 54,000 variants), served by the
server built from this change with its own job runner. The catalog's export
(6,790,899 bytes, 54,004 rows) was sent back as an import unchanged:

| Moment | Observed |
|---|---|
| the POST | 202 in 97 ms, the file checked whole and counted |
| first run | started at the next minute's tick |
| while a run works | about 2,700 rows every 20 s, 136 a second; a run's 40 s apply about 5,400 |
| the end | 54,004 rows done in 9 min 38 s; 0 created, 0 updated, 0 failed |

An unchanged row costs its product read with its relations, about seven
milliseconds, and writes nothing.

## 3. Rows that change

The first 2,000 rows of the same export with a suffix on each title, sent as a
second import: 2,000 updated in 30 s from the run's start, about 66 rows a
second, each an update and its event.

The copy was dropped afterwards.

## 4. Mutations

| # | Mutation | Killed by |
|---|---|---|
| I1 | a row recorded twice counted twice | integration |
| I2 | the file kept after the end | integration (the CHECK) |
| I3 | an unknown column accepted | the service's test |
| I4 | the export's apostrophe kept | the service's test |
| I5 | no lookup by handle | the service's test |
| I6 | no lookup by SKU | the service's test |
| I7 | an unchanged product written | the service's test |
| I8 | the run's deadline ignored | the service's test |
| I9 | a refused row stopping the rest | the service's test |
| I10 | any media type taken | the API's test |
| I11 | no size bound | the API's test |
| I12 | the run's budget past its bound | the job's test |
| I13 | the job not registered | the installation's job test |
| I14 | options compared with `strings.EqualFold` | the service's test |
| I15 | the import's body described as an object | the end-to-end schema test |

I6 survived the first run: every test's variant had options, and a variant not
found by its SKU was found by them. A product with no options has only its SKU
to be found by, and a test now imports one.

I13 is caught because the installation's job test named three jobs; the
scheduled publisher and this job are now among the names it checks.

I14 is the comparison staticcheck proposes. It is not the product module's:
`resolveByTitle` lowers both sides, and U+0130, the capital I with a dot,
lowers to "i" and does not fold to it. With the mutant, a row naming an
existing variant by such a value made a second variant; the comparison keeps
`strings.ToLower` under a reasoned exemption, and a test holds it.

I15 is held by a branch the end-to-end schema test gained here. The test took
every request body for a JSON object with properties and failed on the
import's CSV body in the integration lane; a CSV request body is now checked
as the export's CSV response is, a string schema under `text/csv`.
