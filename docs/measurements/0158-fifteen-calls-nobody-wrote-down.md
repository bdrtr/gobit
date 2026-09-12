# Fifteen calls nobody wrote down — measured 2026-09-12

What an operator must create before a shopper can buy, and where that knowledge
was kept.

## 1. The question, and why it was open

A8.13's measurement stopped on it: "who creates the REGION? on a fresh install
`POST /store/v1/carts` can resolve no country". The chain is short and each link
refuses cleanly:

- `POST /store/v1/carts` carries no region and no currency; the server derives
  both from the country code.
- The cart workflow asks the region module `RegionIDForCountry`.
- The region service answers with a named refusal when the country is bound to no
  region — the code says `the operator has not opened sales to that country`.

The region module's migrations seed currencies and countries and NO region:

```
$ grep -h "INSERT INTO" internal/modules/region/migrations/*.up.sql
INSERT INTO currency (code, symbol, name, decimal_digits) VALUES
INSERT INTO country (iso_2, name) VALUES
```

And nothing else creates one:

```
$ grep -rl "region\|Region" internal/rig/*.go | grep -v _test
(nothing)
```

`gobit seed` resolves the auth service, ensures a sales channel and calls
`rig.Seed`. Its own godoc says what it is for — rebuilding the load rig, fifty
two thousand products in bulk SQL, "no events, so no search index". It is a
measurement tool and creating a region is not its job.

## 2. Where the knowledge actually lived

Two harnesses, each doing the work itself:

- `internal/smoke`'s storefront scenario: `openStorefrontRegion` (region + two
  countries), `setUpStorefrontCatalog` (product, variant, price set, price
  binding, stock location, inventory item, inventory binding, level).
- `internal/e2e`'s harness: its own regions, tax and catalog.

So both lanes were green and neither could see the gap. The knowledge was
correct, executable and unreadable: an operator would have had to read two test
files to learn the sequence.

## 3. The sequence, measured by running it

Fifteen calls. Four were documented (`security.md`'s block) and eleven were not.

| # | Call | Documented before |
|---|---|---|
| 1 | the first admin, through the bootstrap environment | yes |
| 2 | `POST /admin/v1/auth/login` | yes |
| 3 | `POST /admin/v1/regions` | no |
| 4 | `POST /admin/v1/regions/{id}/countries` | no |
| 5 | `POST /admin/v1/tax-regions` | no |
| 6 | `POST /admin/v1/tax-rates` | no |
| 7 | `POST /admin/v1/sales-channels` | yes |
| 8 | `POST /admin/v1/api-keys` (publishable, bound to the channel) | yes |
| 9 | `POST /admin/v1/products` | no |
| 10 | `POST /admin/v1/products/{id}/variants` | no |
| 11 | `POST /admin/v1/price-sets` | no |
| 12 | `PUT /admin/v1/variants/{id}/price-set` | no |
| 13 | `POST /admin/v1/stock-locations` | no |
| 14 | `POST /admin/v1/inventory-items` + `PUT …/variants/{id}/inventory-item` | no |
| 15 | `POST /admin/v1/inventory-items/{id}/levels` | no |

Then the shopper: open a cart, add a line, read the total, complete.

Four of the eleven are BINDINGS, and each has a silent failure:

- no country bound → every cart refused, for every country code there is;
- no price binding → the line cannot be added, and the storefront sends no
  amount, so there is no way around it;
- no inventory binding → the variant is counted out of stock and the cart can
  never become an order;
- no level → the item has nothing anywhere.

## 4. What running it found

Two things the document was wrong about before it was executed.

**`expected_total` is mandatory on completion.** The first run of the block
answered:

```
422 cart_invalid_request: expected_total is mandatory; the total approved by the
customer has to be declared
```

The amount the customer approved is declared, and a cart whose total has moved
since is refused with 409 rather than charged. Both answers were produced by the
block, in that order, because the first fix was the wrong number.

**A one-country region is NOT taxed by its own rate.** The block created a region
with `automatic_taxes: true` and `tax_rate_bps: 2000`, and the cart came back:

```
subtotal 64000   tax_total 0   total 64000
```

The same amounts through the smoke scenario's own region helper:

```
subtotal 64000   tax_total 12800   total 76800
```

The only difference is that the helper binds TWO countries. The cart flow asks
which country the region resolves to: exactly one country means the TAX MODULE is
the authority and its answer is taken as it is — including "this country has no
tax region", which is zero. No single country means no jurisdiction can be named,
and the flow falls back to the region's rate.

So the smoke scenario, whose assertion says "the tax must be computed with the
region's rate", exercises the FALLBACK. The ordinary case — one country — went
through the other branch and no lane had ever taken it.

It is not a defect. The server warns:

```
WARN the country's tax region is not configured; the tax was computed as zero
     cart_id=… country_code=TR tax_source=tax_unconfigured
```

and the reasoning against sliding back to the region whenever tax is
unconfigured is written where the choice is made. What was missing was a document
telling an operator to configure the tax module.

## 5. The gate that already existed

A population gate for this was written here — and then deleted, because the tree
already had one: `TestEveryChainedCurlFlowIsExecuted`, holding a
document-to-witness map whose keys are derived from the documents. It failed the
moment the new document appeared, which is exactly what it is for, and registering
the witness was the whole of the work.

The search that missed it is worth writing down. The smoke package was read, the
word "documented" was searched, and `internal/arch` was not searched for a
population rule over DOCUMENTS. The existing gate carries the word "curl" in its
name and was one grep away.

Two gates over one population would have been worse than one: the weaker sets the
standard, and a reader cannot tell which is the rule.

### What the discarded version taught anyway

Its first subject was any `curl` at the documented address, and it immediately
found a third document — `api-surfaces.md`, two blocks with a single call each,
one carrying `pk_…` where a key belongs. Neither is a flow and one cannot run at
all. The existing gate draws the line in the same place, on the CHAIN, and its
godoc gives the same reason.

Then the discarded check had the defect it existed to catch. It looked for the
document's NAME anywhere in the smoke sources — and a scenario's godoc mentions
the document it runs, so splitting the constant until the literal disappeared left
it GREEN:

```go
const firstRunDoc = "../../docs/" + "first" + "-run.md"   // the check still passed
```

That is the sixth instance in this session of a check auditing something adjacent
to its claim, and it was found the only way any of them were: by running the
mutation instead of reasoning about it.

## 6. Mutations

| Mutation | What failed |
|---|---|
| the document's reference split so no literal remains | the population gate — after its subject was fixed; before that it SURVIVED |
| the chain removed from the document (addresses spelled `http://localhost:9000`) | the population gate's count: the document stopped being a flow |
| `expected_total` dropped from the completion body | the smoke scenario, on the order assertion: the completion answers 409 and the id is `null` |
| the tax rate step dropped | the smoke scenario, on the LINE COUNT — seven printed lines against eight. The blindness guard fires before the content assertions, which is the order it is meant to fire in: a block that changed shape is read at the wrong positions, and asserting the total against the wrong line would be worse than not asserting it |

### A mutation that applied nothing

The first attempt at the `expected_total` mutation replaced a string that was not
in the file — the document writes that field inside a single-quoted shell body, and
the replacement was written for the escaped form used elsewhere in the block. It
silently changed nothing.

The run then FAILED anyway, for an unrelated reason: an earlier restore had
dropped the tax rate line, so the total was 64000 and the completion was refused.
A mutation that applies nothing and a failing test in the same minute is the worst
shape available — it reads as proof and is not. The anchor has to be verified
before the result is believed, which is why both mutations above were re-run with
an assertion that the replacement matched at all.
