# What a catalog page is made of — measured 2026-10-05

Evidence for [ADR 0391](../adr/0391-a-catalog-read-answers-a-revalidation.md).
Read from the tree at 4cd5ec7f; the reproductions ran in this worktree before
the record landed.

## 1. The census: what moves a storefront product body

A storefront product (`service.StoreProduct`) is the product row and its
children, enriched with two other modules' records through the Query layer, and
filtered by the request's channel. ADR 0222's version is bumped by
`Service.revise`, which the product, variant, image, attribute, bundle and
revision writes call (`internal/modules/product/service`, `s.revise(` in
`product.go`, `variant.go`, `image.go`, `attribute.go`, `bundle.go`,
`revision.go`); nothing in `links.go` or `taxonomy.go` calls it.

| Part of the body | Owner | Moves the version |
|---|---|---|
| Title, handle, description, status, dimensions, metadata | product | yes |
| Variants and options | product | yes |
| Images | product | yes |
| Attribute values | product | yes |
| Tags and categories attached by an update | product | yes |
| A tag or category deleted from the vocabulary | product (`DeleteTag`, `DeleteCategory`) | no |
| A variant's price set link, inventory item link | product (`links.go`) | no |
| A product's sales channel link | product (`links.go`) | no |
| The warehouses serving the channel (ADR 0092) | link service | no |
| `price_set` and its prices | pricing | no — pricing publishes no version |
| A price list window opening or closing | pricing, against the clock | no write at all |
| A reduction's reference days | pricing, against the clock | no write at all |
| `inventory_item.available_quantity`, `in_stock` | inventory | no — inventory publishes no version |
| A reservation | inventory | no |

The enrichment reads these in separate queries, not in one snapshot, so a body
read across a concurrent write can mix the two sides of it; the tag is computed
over what was assembled, so it describes that body and no other.

## 2. The reproduction: three writes the version does not see

`internal/e2e/catalog_validator_test.go` (`TestAProductPageRevalidatesAcrossModules`),
run once with two temporary log lines reading the admin product's `ETag`
(ADR 0222's version) before and after each write:

```
MEASURE a tag removed from the vocabulary: admin ETag "3" -> "3"
MEASURE a tag removed from the vocabulary: storefront ETag -> "126c9b9b9313e9505638c9c1f5142f31", listing ETag -> "139710c310ca7a9cd87f610278cf4303"
MEASURE a stock level: admin ETag "3" -> "3"
MEASURE a stock level: storefront ETag -> "feb3e1e8d36c218722fa8a02b604f36c", listing ETag -> "b0630bdac2bd4d2f56b36df8bc00ffef"
MEASURE a price: admin ETag "3" -> "3"
MEASURE a price: storefront ETag -> "6bc925f40f0bd3666387382d50d4fd5c", listing ETag -> "9891dea129f721ee1c1deab0dff6a30a"
--- PASS: TestAProductPageRevalidatesAcrossModules (0.04s)
```

The version stays at 3 across all three writes while the storefront body, and
the listing carrying it, move each time. A tag built from that version would
have answered 304 with the old tag, the old stock and the old price.

## 3. The cost of the hash

A synthetic listing of 100 products (three variants each, prices, stock,
images, tags, a category and a long description), about 442 KiB encoded; Go
1.26.6, `GOMAXPROCS=2`, AMD Ryzen 7 8845HS, `-benchtime=300x -count=3`:

| Step | ns/op |
|---|---|
| `json.Encoder.Encode` alone | 956 174 – 978 737 |
| `sha256.Sum256` over the body (≈ 2.4 GB/s) | 180 760 – 195 338 |
| `corehttp.WriteJSON` | 1 124 669 – 1 197 447 |
| `corehttp.WriteJSONWithValidator` | 1 290 649 – 1 311 641 |

Run for run, `WriteJSONWithValidator` took 0.11, 0.13 and 0.17 ms more than
`WriteJSON` (1 311 641 − 1 197 447, 1 300 278 − 1 166 189, 1 290 649 − 1 124 669
ns/op) and two more allocations per op in each pair (8 726/8 727 against
8 724/8 725). The hash alone is 0.18–0.20 ms, about a fifth of the encoding's
cost; the three differences come out below it, and this run does not say why.
What it is a share of — the whole REST response with its reads and enrichment
on the `gobit_load` fixture — was not measured here; that comparison is the
trigger to revisit if a listing's response time ever shows the hash.

The benchmark was a temporary file in `core/http` and is not in the tree; it
encodes `map[string]any` records of the shape above and times the four steps.

## 4. The defect the old test could not see (D241)

ADR 0151's policy test, removed by this record, walked a hand-written map of
three addresses. Six handlers wrote the policy. Deleting the policy line from
each of the three unlisted handlers, one at a time, with
`go test -count=1 ./internal/modules/product/... ./internal/arch/`:

| Handler | Route | Result |
|---|---|---|
| `storeRelatedProducts` | `.../products/{id}/related` (ADR 0180) | every test green |
| `storeFacets` | `.../product-facets` (ADR 0219) | every test green |
| `storeAddOns` | `.../products/{id}/add-ons` (ADR 0228) | every test green |
| `storeListProducts` (control) | `.../products` | the policy test fails |

## 5. The gates and the mutants they kill

Each mutant ran against a green base with `go test -count=1` on its package
(`-run` named where one test is the subject). All 42 were killed.

| # | Mutant | File | Killed by |
|---|---|---|---|
| C1 | tag over the body minus its last byte | `core/http/response.go` | `TestTheTagIsTheBodysOwnHash` |
| C2 | weak tag | `core/http/response.go` | `TestAMatchingTagIsAnsweredWithNoBody`, `TestEveryMemberIsSearched`, `TestTheTagIsTheBodysOwnHash` |
| C3 | body on the 304 | `core/http/response.go` | `TestAMatchingTagIsAnsweredWithNoBody` |
| C4 | ETag dropped from the 304 | `core/http/response.go` | `TestAMatchingTagIsAnsweredWithNoBody` |
| C5 | Content-Type on the 304 | `core/http/response.go` | `TestAMatchingTagIsAnsweredWithNoBody` |
| C6 | 304 sent unconditionally | `core/http/response.go` | `TestADifferentTagGetsTheBody`, `TestAMatchingTagIsAnsweredWithNoBody`, `TestEveryMemberIsSearched`, `TestTheComparisonIsWeak`, `TestTheTagIsTheBodysOwnHash` |
| C7 | strong comparison | `core/http/response.go` | `TestTheComparisonIsWeak` |
| C8 | Get instead of Values | `core/http/response.go` | `TestEveryMemberIsSearched` |
| C9 | only the first member read | `core/http/response.go` | `TestEveryMemberIsSearched` |
| C10 | star ignored | `core/http/response.go` | `TestAStarMatches` |
| C11 | tag set before the body is encoded | `core/http/response.go` | `TestAnUnencodableValueIsStillA500` |
| C12 | Vary added on the 304 | `core/http/response.go` | `TestTheQueryStringIsPartOfTheKeyAndTheHeaderDoesNotSayOtherwise` |
| K1 | CORS does not allow If-None-Match | `core/http/cors.go` | `TestABrowserStorefrontCanAskPermission` |
| K2 | CORS does not expose ETag | `core/http/cors.go` | `TestABrowserMayReadTheTag` |
| O1 | caller 200 map mutated | `core/openapi/schema.go` | `TestRevalidatedDescribesTheTagWithoutTouchingItsInput` |
| O2 | caller responses map mutated | `core/openapi/schema.go` | `TestRevalidatedDescribesTheTagWithoutTouchingItsInput` |
| O3 | no 304 described | `core/openapi/schema.go` | `TestRevalidatedDescribesTheTagWithoutTouchingItsInput` |
| O4 | no header parameter | `core/openapi/schema.go` | `TestRevalidatedDescribesTheTagWithoutTouchingItsInput` |
| O5 | no ETag header on the 200 | `core/openapi/schema.go` | `TestRevalidatedDescribesTheTagWithoutTouchingItsInput`, `TestRevalidatedKeepsTheHeadersThe200AlreadyDescribes` |
| O6 | append writes through the caller array | `core/openapi/schema.go` | `TestRevalidatedDescribesTheTagWithoutTouchingItsInput` |
| O7 | headers the 200 already describes discarded | `core/openapi/schema.go` | `TestRevalidatedKeepsTheHeadersThe200AlreadyDescribes` |
| O8 | caller's 200 headers map written through | `core/openapi/schema.go` | `TestRevalidatedKeepsTheHeadersThe200AlreadyDescribes` |
| O9 | a 200 with no headers map left without one | `core/openapi/schema.go` | `TestRevalidatedDescribesTheTagWithoutTouchingItsInput` |
| P1 | D241: related products lose the policy | `internal/modules/product/api/relation.go` | `TestEveryCatalogReadCarriesItsValidator` |
| P2 | D241: facets lose the policy | `internal/modules/product/api/attribute.go` | `TestEveryCatalogReadCarriesItsValidator` |
| P3 | D241: add-ons lose the policy | `internal/modules/product/api/add_on.go` | `TestEveryCatalogReadCarriesItsValidator` |
| P4 | a product read through writeItem (policy kept, no tag) | `internal/modules/product/api/store.go` | `TestEveryCatalogReadCarriesItsValidator`, `TestTheTagFollowsTheAnswerNotTheAddress` |
| P5 | tags read through writeList | `internal/modules/product/api/store.go` | `TestEveryCatalogReadCarriesItsValidator` |
| P6 | facets through plain WriteJSON | `internal/modules/product/api/attribute.go` | `TestEveryCatalogReadCarriesItsValidator` |
| P7 | policy on a taxonomy read (collections) | `internal/modules/product/api/store.go` | `TestEveryCatalogReadCarriesItsValidator`, `TestTheUNSCOPEDStoreReadsCarryNoPolicy` |
| P8 | add-ons placeholder renamed | `internal/modules/product/api/add_on.go` | `TestEveryCatalogReadCarriesItsValidator` |
| P9 | tag taken from the address | `core/http/response.go` | `TestTheTagFollowsTheAnswerNotTheAddress` |
| P10 | validator answered before the product read | `internal/modules/product/api/store.go` | `TestARefusalIsNeverValidated` |
| P11 | validator answered before the channel is asked | `internal/modules/product/api/store.go` | `TestAForeignChannelIsRefusedWhateverTheTag` |
| P12 | tag only when a TTL is set | `internal/modules/product/api/catalogcache.go` | `TestTheDefaultWritesNoFreshnessPolicy` |
| P13 | a store read described without Revalidated (tags) | `internal/modules/product/api/describe.go` | `TestEveryStoreReadDescribesItsValidator` |
| S1 | search back to WriteJSON | `plugins/searchpg/api.go` | `TestASearchAnswersARevalidation` |
| S2 | search described without Revalidated | `plugins/searchpg/describe.go` | `TestTheSearchDescribesItsValidator` |
| A1 | arch: search back to WriteJSON | `plugins/searchpg/api.go` | `TestEveryChannelScopedRouteAnswersWithTheValidator` |
| A2 | arch: related products through writeItem | `internal/modules/product/api/relation.go` | `TestEveryChannelScopedRouteAnswersWithTheValidator` |
| A3 | arch: the error-path audit not widened for the validated writer | `internal/arch/error_path_test.go` | `TestErrorResponsesAreWrittenInOnePlace` |
| A4 | arch: constant-time exemption removed | `internal/arch/constant_time_test.go` | `TestASecretIsComparedInConstantTime` |

The end-to-end test (`TestAProductPageRevalidatesAcrossModules`) and the
in-process keyless request were not mutated: the e2e package ran once, whole,
and the root package's integration tests were vetted and not run.

## 6. Notes

- `measurements/0151` still names the search at `/store/v1/search`; the route
  moved under the channel segment in 25788858 and is
  `/store/v1/sales-channels/{sales_channel_id}/search`.
- `GET /files/{key}` already answered 304 through `http.ServeContent`, so a 304
  in the request log and the telemetry is not new: the logger writes any status
  below 400 at info and the telemetry marks only 500 and above as an error.
