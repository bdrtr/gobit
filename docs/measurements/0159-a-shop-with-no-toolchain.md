# A shop with no toolchain — measured 2026-09-12

What a guest browser can do against gobit, what an example costs, and what the
tree would have let in.

## 1. The guest surface

Forty-eight routes under the storefront prefix, derived from the modules' route
registrations rather than counted from prose. Every one of them requires the
`x-publishable-api-key` header — the store prefix is bound with a NIL exempt
list, so there is no open store route at all.

Thirty-seven are usable by a guest. The other eleven refuse one: the eight under
`/store/v1/customers/{id}` and the two b2b routes reach a proven-customer check
and answer 401 when no identity is bound, which on a stock installation is
always.

So "guest" is not a mode the storefront has. It is the only state the store
surface knows, unless an embedder binds an identity — which is what
`examples/starter` demonstrates and this example deliberately does not.

## 2. The three pages, and where each value comes from

| Page | Call | Where the channel comes from |
|---|---|---|
| catalog | the channel-scoped product list, with `limit=20` | the PATH |
| product | the same list endpoint plus the product segment, which accepts a handle | the PATH |
| cart | `POST /store/v1/carts`, then the line-items endpoint, then the cart read | the KEY |

Two facts decided the example's configuration:

- The catalog endpoints read the channel from the path SEGMENT and never from a
  query parameter, so the shop has to be told which channel it is.
- The cart endpoints read it from the principal — that is, from the key — so the
  cart never names it.

And the cart's body carries the COUNTRY alone: `currency_code` and `region_id`
are refused with 422, because the server derives both. The example asks
`GET /store/v1/regions` for a country rather than hard-coding one, so a shop
configured for anywhere works without editing the script.

## 3. What no endpoint can tell the shop

```
$ grep -rn "store/v1" --include='*.go' internal/modules/*/api/*.go \
    | grep -v _test | grep -oE '"/store/v1[a-z0-9/{}_-]*"' | sort -u | wc -l
45
```

None of them lists sales channels and none returns a publishable key. The key is
returned once, by the admin endpoint that mints it. So both values are
configuration, and `docs/first-run.md` step 5 is where they come from — which is
the reason that document had to exist before this example could.

## 4. What the tree would have let in

Three things the measurement found, each verified here.

**A `.js` file's prose was read by nothing.** `scannedExtensions` held `.go`,
`.sql`, `.gohtml`, `.md`, `.graphqls` and `.tmpl`. The repository was already
shipping two hand-written scripts — the panel's review screen and the analytics
funnel — whose content the language scan had never opened. A script's prose is
the most VISIBLE prose the repository has: it is what an operator reads inside
the page.

Closed in this change. Mutation: a Turkish comment in `reviews.js` now fails the
lane, and did not before.

**A separate module's `go` directive is tied to nothing.** No gate reads
`examples/*/go.mod`. A module declaring `go 1.25` against a root of `go 1.26.6`
passes the whole arch suite — measured by an agent that wrote one. Left open and
recorded: the directive being lower is not a defect in itself, and a gate for it
would be a new rule rather than a fix.

**Separate modules are not linted in CI.** The Lint job runs the root through
`golangci-lint-action` and then `make vuln`; it does not run `make lint`, which
is the target that loops the separate modules. Their only linter is the pre-push
hook, and `git push --no-verify` skips it. Recorded as D100; it is the same shape
as D88, where a hundred and thirty-five files sat outside every fast check.

## 5. What the gates demanded, in the order they said it

The example was written and then the lanes taught it the house rules:

1. `TestTheModuleListCoversEveryModuleOnDisk` — "examples/storefront declares a
   Go module and is not in the Makefile's SEPARATE_MODULES". Derived from disk,
   so the Makefile could not be forgotten.
2. `TestNonModuleHTTPSurfacesWriteThroughTheCore` — two `http.Error` calls,
   named with their line and their reason: the body would not be the shared
   envelope, the request id would never reach the response, and the message
   would go out unmasked. Both became `corehttp.WriteError` with a code of their
   own.
3. `TestTheOutOfTreeExamplesCompile` — only once the table entry was added, and
   that turned out to be the fourth finding.

The first two are the interesting ones: neither is about storefronts, and both
caught an example written by somebody who had read the panel's code all day.

## 5b. The table that was bound to nothing

`outOfTreeExamples` is typed by hand and nothing tied it to disk. Mutation:
removing the fresh entry left the whole arch suite GREEN. A module added to the
Makefile and forgotten in the table is run by `test-modules`, `lint` and `vuln` —
all of which execute INSIDE the module, where a program that reached `internal/`
through the replace directive would pass — and never by the one lane whose whole
subject is that `core/` and the facade are enough.

Closed in this change: the table's population now comes from the Makefile's
`SEPARATE_MODULES`, which another gate already holds to the `go.mod` files on
disk. The chain is disk → Makefile → table, and both directions bite.

The measurement had also said the Makefile's COUNT is a floor with no gate.
That is wrong, and the mutation says so: leaving `SEPARATE_MODULE_COUNT` at five
while the list grew to six fails `TestTheModuleListCoversEveryModuleOnDisk`,
which compares them.

## 6. Sizes

| File | Lines |
|---|---|
| `storefront/assets/storefront.js` | 299 |
| `storefront/pages.go` | 182 |
| `storefront/storefront.go` | 125 |
| `README.md` | 57 |
| `main.go` | 55 |
| `storefront/templates/page.gohtml` | 53 |

Against the neighbours: the panel's review screen is 252 lines of script, the
analytics funnel 102, and `examples/starter` is 380 lines of Go.

## 7. What this did not do

No payment, no address, no shipping, no customer. Each is an endpoint the
example does not call, and the cart page says so on the page rather than only in
the README — a shopper who reaches the end of this shop is told where the rest
is, which is `docs/first-run.md`.
