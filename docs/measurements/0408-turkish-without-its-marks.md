# Turkish without its marks — measured 2026-10-06

The evidence behind [ADR 0408](../adr/0408-turkish-without-its-marks-is-read-in-go-text.md)
and gap D263. Taken on the tree at `78de3fb4` (ADR 0404, ADR 0405, ADR 0407).
Files are named, lines are not, because they move. The Turkish words found are
not quoted here: the word lane reads this file too, and a count says what a
quotation would.

## 1. What the three lanes missed

The new lane, run over the unchanged tree with the word list's five additions,
found 185 words in 24 Go files. The same lane scanner, kept outside the tree,
listed every hit rather than the first per file:

| File | Hits | What they were |
|---|---|---|
| `internal/modules/promotion/service/service_test.go` | 55 | promotion and campaign codes and names, invalid enum values |
| `internal/modules/promotion/service/compute_test.go` | 23 | promotion codes, campaign names |
| `internal/modules/promotion/api/api_test.go` | 21 | codes in request bodies and paths, metadata |
| `internal/modules/promotion/service/redeem_test.go` | 18 | codes |
| `internal/modules/promotion/promotion_integration_test.go` | 14 | codes, campaign names, a reference, metadata |
| `internal/modules/promotion/service/interop_test.go` | 9 | codes |
| `internal/rig/catalog.go` | 7 | the rig's warehouse, handles, titles and a variant title |
| `internal/modules/b2b/b2b_integration_test.go` | 7 | an e-mail domain and an upper-case local part |
| `plugins/searchpg/searchpg_integration_test.go` | 5 | indexed titles and the queries for them |
| `internal/arch/doc_references_test.go` | 4 | names quoted from records, one example comment |
| `internal/modules/b2b/service/service_test.go` | 3 | an e-mail domain |
| `plugins/paymentpaytr/provider.go` | 2 | the provider's API paths |
| `internal/rig/rig.go` | 2 | the rig's handles, in its godoc |
| `internal/modules/inventory/service/service_test.go` | 2 | an error code, a warehouse name |
| `internal/modules/inventory/service/provider_test.go` | 2 | an unknown field's name |
| `internal/arch/build_files_test.go` | 2 | the incident's test selector |
| `core/http/redisguard/redisguard_integration_test.go` | 2 | a stored body's id |
| `plugins/searchpg/searchpg_internal_test.go` | 1 | a variant title |
| `internal/modules/product/service/internal_test.go` | 1 | a slug test's expected handle |
| `internal/modules/payment/payment_integration_test.go` | 1 | a comment naming a helper deleted in b6400c9b |
| `internal/modules/inventory/inventory_integration_test.go` | 1 | an error code |
| `internal/modules/inventory/api/api_test.go` | 1 | an unknown field's name |
| `internal/modules/fulfillment/manual/manual.go` | 1 | the error message an API caller receives |
| `core/query/query_integration_test.go` | 1 | an unknown filter's name |

Reading them found Turkish the lane does not: a whole Turkish comment sentence
in the search plugin's integration test, a fingerprint prefix in the guard's
test (23 uses), a promotion id padded with a Turkish word, a stock location's
name in three inventory API tests, a request path in the HTTP middleware test,
and an error code. They were translated with the rest.

## 2. What the first version of the lane flagged that was not Turkish

- `AC`: the stem list's one two-letter stem matched the first part of "ACKed"
  and "ACKing" in the event bus's comments and the configuration's godoc, and a
  base64 test vector in the web push plugin. Eight files. Parts of two letters
  are passed over.
- Escaped Turkish input in the case-folding tests of the e-mail gate, the
  invoice refold and the product slug, read once the literal was decoded. The
  lane reads a literal as written with its escapes blanked, which is the letter
  lane's existing policy for escaped data.
- The file names the path ledger lists, named in comments and in the reference
  audit's strings. The lane subtracts their base names.

## 3. The five words added to the safe list

Each is a suffixed form the stem list cannot match, found in this tree, and
each was counted as a whole word, case folded, over the 7710 `.go` files of the
go1.26.6 standard library: zero hits for every one. They are listed, with that
note, beside the list in `internal/arch/language_test.go`.

## 4. Outside the lane

- The promotion module's first migration gave a Turkish example code in a
  comment; the comment was translated, and the lane does not read SQL, so
  nothing holds a SQL comment to it.
- The rig's titles, which its package keeps character for character so that a
  rebuilt rig diffs clean against the measured one, carry an exemption.
- Turkish that is no stem and none of the fifteen words, such as a proper
  name, passes every lane. The list is a floor.
