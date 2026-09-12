# The second door — measured 2026-09-12

What the admin panel asked of an operator before ADR 0156, and what the API
asked of the same operator for the same data.

## 1. The API's side

Every admin endpoint names a scope. The population is derived from the source
rather than counted by hand:

```
$ grep -rn "RequireScope" --include='*.go' internal/ plugins/ \
    | grep -v _test.go | wc -l
71
```

The scope constants themselves live in the module api packages, one pair per
module:

```
$ grep -rn -E 'Scope[A-Za-z]* += .*"' --include='*.go' \
    internal/modules/*/api/*.go plugins/*/*.go | grep -v _test
internal/modules/customer/api/api.go: ScopeRead  = "customer:read"
internal/modules/customer/api/api.go: ScopeWrite = "customer:write"
internal/modules/order/api/routes.go: ScopeRead  = "order:read"
…
```

Forty-one such constants across the modules and the plugins.

That the check is real end to end is not assumed. `internal/e2e`'s
authorization matrix walks the router and, in its own words, "asserts 403 for a
valid identity with no scope" on every admin endpoint. The same file says what
it does NOT cover:

> The panel tree. `/admin/ui` is not mounted in this harness, so its own ring —
> identity, origin and the session cookie ADR 0076 widened — is audited in
> internal/app against the real guard stack instead.

That sentence is true and it is narrower than it reads. What internal/app
audited was identity, origin and the cookie. Privilege was not on the list,
because there was none.

The other audit could not have seen it either: the router-walking authorization
test filters its population with `strings.HasPrefix(pattern, "/admin/v1")`, so
no panel path has ever been in it. Neither gate was wrong. Between them they
left a surface nobody's population contained.

## 2. The panel's side

Everything in this section is the state BEFORE the record; the searches are the
ones that measured it and they answer differently now.

```
$ grep -rl "HasScope\|RequireScope" internal/adminui/*.go | grep -v _test
(nothing)
```

The panel resolved a principal and put it into the context — and then read back
exactly one bit of it:

```
$ grep -rl "PrincipalFromContext" internal/adminui/*.go | grep -v _test
internal/adminui/login.go
internal/adminui/template.go
```

Two files. In `login.go` it is the identity of whoever just signed in; in
`template.go` it is this, which is the whole of what the frame asked:

    _, signedIn := corehttp.PrincipalFromContext(r.Context())

The value was discarded at the assignment. The frame asked whether somebody was
signed in, so that it could decide whether to draw a sign-out button; the scope
list was in the struct, in the context, on every request, and nothing read it.

`RequireAdmin` — the ring both doors authenticate through — requires no scope
either. It authenticates and passes the principal on; on `/admin/v1` a second
middleware then names the privilege, and on `/admin/ui` nothing did.

## 3. Why it was not hypothetical

A narrow operator is a state the auth module supports on purpose:

- `POST /admin/v1/users` takes a `scopes` list (the request DTO's comment says
  "if not given, admin is applied" — so a list that IS given is honoured).
- `PATCH` on a user takes a new `scopes` list; the DTO notes that removing a
  scope is free while adding one is refused without the privilege.
- The service tests cover both directions, including a narrow key refused an
  escalation.

So `{"scopes": ["order:read"]}` creates an account that the API answers 403 for
on customers, inventory and the catalog — and that the panel answered with the
customers' names, emails and addresses, the full catalog, the stock levels and
the sales report.

And not only answered. The panel WRITES through the module admin surfaces it
resolves optionally: the product edit form, the variant price and the variant
stock all call straight through with no authorization of any kind. An operator
the API refused `product:write` could change a product's title through the
panel.

The grant itself is live rather than cached: an admin token's scopes are
re-read from the user row on every request, and the JWT's own `scopes` claim is
documented as "only a copy that serves the client in drawing its interface".
So narrowing a grant takes effect on the next click, and the panel's refusal is
as current as the API's.

The read layer offered no second line of defence:

```
$ grep -rl "Principal\|HasScope\|Scope" core/query/*.go | grep -v _test
(nothing)
```

The panel's screens read through `core.query` (its `ServiceQuery` constant
resolves exactly that), so the data arrived unfiltered by anything about who
asked.

## 4. The evidence that was already in the tree

Five of the panel's own tests build an operator carrying NO scope and assert
the screen renders:

```
$ grep -rh "corehttp.Principal{" internal/adminui/*_test.go internal/app/*_test.go \
    | grep -vc "Scopes"
16
```

Of those sixteen, four are `Principal{ID: "user_1", Kind: "user"}` handed to
the orders, customers, sales and inventory screens, a fifth is the same shape
under another id in the session tests, and one is the composition root's fake
authenticator — the one the CSP walk and the plugin-screen test both sign in
with:

```go
// internal/app/setup_test.go, before this record
return corehttp.Principal{ID: "usr_panel", Kind: "user"}, nil
```

Nothing was wrong with those fixtures. A fixture carries what the code reads,
and the code read nothing.

## 5. What the screens cost now

| Screen | Privilege | Why that one |
|---|---|---|
| Catalog, product, variant | `product:read` | the product module's own read scope |
| Product edit (form and submit) | `product:write` | a form an operator cannot submit is a screen that wastes their time |
| Variant price | `pricing:write` | it writes a price, not a product |
| Variant stock | `inventory:write` | it writes a stock level |
| Orders, order | `order:read` | |
| Sales report | `order:read` | it is made of order lines and holds no data of its own |
| Customers, customer | `customer:read` | |
| Inventory | `inventory:read` | |
| Reviews, reviews.js | `review:read` | |
| A registered screen | whatever it registered | the plugin passes the constant its own endpoint requires |

Open, deliberately: the login page on both verbs, the sign-out, the stylesheet
and the entry point. The first two establish identity, the third must work for
anybody who can sign in, the fourth is install-identical bytes the login page
needs, and the fifth holds no data — it redirects to the first screen the
operator can open, or refuses when there is none.

## 6. The mutations

Fourteen, all of which bit. The four worth keeping:

**The binding line names another screen's path.** Changing one route to
`r.Get(CustomersPath, u.needs(OrdersPath, u.listCustomers))` leaves the
unprivileged operator refused exactly as correctly — the first router walk
stays green. It is the SECOND walk, the one that requests each route as an
operator holding precisely the scope that route's own path is listed under,
that fails. A single walk would have proved the panel checks something.

**A misspelled scope in the panel's table.** `"order:read"` → `"orders:read"`
leaves the behavioural gates green: the menu hides the orders and the router
refuses them, and the two AGREE, which is all those gates compare. Only the
arch gate — which reads the panel's table and the modules' constants from
source and asks whether the value exists at all — bites. A privilege nothing
grants is a screen no narrow operator can ever open, and it compiles.

**A registered screen outside the policy group.** Not this record's mechanism
but its neighbour, and found while widening the walk: ADR 0155's content-policy
gate built the panel with NIL pages, so a plugin's shell and script had never
been in its population. Binding `pageRoutes` outside the group that carries the
policy is a one-line change a future author could make, and with nil pages it
stayed green. With a registered screen in the walk it fails. Recorded as D93.

**The scope not carried across the composition root.** Dropping one line from
`panelPages` (`Scope: registered[i].Scope`) makes every plugin registration
arrive scopeless, which `validatePages` then refuses at startup. It is caught
because the gate builds a REAL `coreplugin.Host`, registers through it and asks
`registerPanel` — the same wire ADR 0155's own last mutation was about.

## 7. What this did not do

The panel's privilege is enforced at the SCREEN. The read layer stays
scope-blind, so a future screen listed under one module's privilege while
reading another module's data would be refused by nothing. The table's godoc
carries that judgment and `internal/arch` can only check that the scope it
names exists — not that it is the right one.
