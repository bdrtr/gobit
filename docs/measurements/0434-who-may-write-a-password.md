# Who may write a password — measured 2026-10-09

Evidence for [ADR 0434](../adr/0434-an-admin-route-demands-a-privilege.md) and
gap D270. Read on a tree at 46801742 (v0.10.0); every Go command ran as
`GOTOOLCHAIN=go1.26.9`, the integration lane against Docker's
`postgres:16-alpine` through testcontainers.

## 1. The fault, from outside

A marketplace built on gobit as a separate process, its own repository, gobit
at the pseudo-version of 46801742, against an installation that binds
`contrib/identity-session` as the starter does. A throwaway customer was
created by the operator, then:

| Caller | `PUT /admin/v1/customer-credentials` | Then |
|---|---|---|
| The marketplace's secret key: nine privileges, none of them the customer module's | 204 | signing in with the written password answered that customer's session cookie |
| A secret key holding only `product:read` | 204 | |

The route was mounted with `r.Put` behind the admin ring and nothing else.

## 2. The admin routes that demand no privilege

The v0.10.0 release audit walked the router of `cmd/server`'s installation
(`gobit.New().InProcess`), once alone and once with the plugins that bind
routes, and read the privilege each route's guards demand. The only routes
under `/admin/v1` without one were the auth module's:

- `GET /admin/v1/auth/me`, `GET /admin/v1/auth/sessions`
- `POST /admin/v1/auth/login`, `/accept-invitation`, `/logout`
- `POST /admin/v1/auth/mfa`, `/mfa/confirm`, `/mfa/remove`
- `POST /admin/v1/auth/sessions/revoke-others`, `/sessions/{id}/revoke`

The probe built neither contrib module, so the credential route was outside
it. The plugins' admin routes (`analytics`, `payment-paytr`, `search-pg`,
`webhook-out`, `web-push`) each carry a guard on the route.

## 3. The fault, on the module's own router

The first fix's two contrib tests, one reading the guard off the route and
one sending the request, with its module change reverted (they were renamed in
the review round, section 6):

```
the guard read off the route
    expected: []string{"customer:write"}
    actual  : []string(nil)
a request nobody authenticated
    expected: 401  actual: 204;  Should be zero, but was 1
a key holding product:read
    expected: 403  actual: 204;  Should be zero, but was 1
    Received unexpected error: identity-session: the password does not match
```

The subtests writing as `customer:write` and as `admin` passed before and
after; the review round then refused `customer:write` (section 6).

## 4. The gate on the installations this tree ships

`TestEveryInstallationThisTreeShipsDemandsAPrivilegeOnEveryAdminRoute` opens
the default installation and the catalog's every plugin, once per error
reporter since the two share one slot; all three pass, and every exempt route
is bound. With the gate's call taken out of the assembly,
`TestAnUnscopedAdminRouteStopsTheInstallationThatBindsIt` failed twice with
"An error is expected but got nil": a module's `PUT` and a plugin's `POST`
under `/admin/v1`, neither guarded, opened an installation.

## 5. The starter on the first fix

`examples/starter` built twice and started against an empty
`postgres:16-alpine`. With the credential route's guard reverted and the gate
in place, the binary exits 1 before listening:

```
fatal: admin_route_unscoped: the installation does not start: 1 route(s) under
/admin/v1 demand no privilege, so any operator or API key could call them:
PUT /admin/v1/customer-credentials (served by
github.com/bdrtr/gobit/contrib/identity-session.(*Module).putCredential-fm). ...
```

With both, over HTTP, for a customer the owner created:

| Caller | `PUT /admin/v1/customer-credentials` |
|---|---|
| A secret key holding only `product:read` | 403 `forbidden`, "requires the \"customer:write\" privilege" |
| No credential | 401 `unauthenticated` |
| The bootstrapped owner (`admin`) | 204 |

## 6. The review round

An independent review of the first fix found that `customer:write`, which it
had chosen, is the grant of every operator who corrects an address in the
panel and of every key that onboards customers, and that the credential lets
its holder read what that grant never could: the customer's orders,
addresses and wishlist through the storefront. It found five shapes the gate
passed and a `product:read` key could write through: a router's not-found and
method-not-allowed handlers, `/{surface}/v1/…`, `/*` and `/admin/v1*`; an
exempt route bound again by a later module; and a module's route under
`/admin/ui`.

With the route moved to `/admin/v1/customer-credentials/{customer_id}` and the
guard still `customer:write`:

```
TestTheCredentialRouteDemandsItsOwnPrivilege
  an operator holding customer:write: expected 403, actual 204; written once
  an operator holding customer-credential:write: expected 204, actual 403
TestTheCredentialRouteDemandsThePrivilegeItPublishes
  expected: []string{"customer-credential:write"}  actual: []string{"customer:write"}
```

The new gate tests against the first fix's gate, and the fallback tests with
no fallback claimed:

```
TestARouteWhoseFirstSegmentIsAParameterIsAnAdminRoute  An error is expected but got nil
TestARootCatchAllIsAnAdminRoute                        An error is expected but got nil
TestAWildcardOnThePrefixIsAnAdminRoute                 An error is expected but got nil
TestAnExemptRouteServedByAnotherHandlerIsRefused       An error is expected but got nil
TestARouteUnderThePanelsAddressMustBeThePanels         An error is expected but got nil (4 of 4)
TestAModulesNotFoundDoesNotAnswerAnAdminPath           expected: 404 actual: 204 (3 paths)
TestAModulesMethodNotAllowedDoesNotAnswerAnAdminPath   expected: 405 actual: 204
```

The starter on the follow-up, against an empty `postgres:16-alpine`, for a
customer the owner created, with keys the owner minted over the API:

| Caller | `PUT /admin/v1/customer-credentials/{customer_id}` |
|---|---|
| `product:read` | 403, "requires the \"customer-credential:write\" privilege" |
| `customer:write` | 403, the same |
| `customer-credential:write` | 204 |
| The owner (`admin`) | 204 |
| No credential | 401 |
| The owner on the old path | 404 `route_not_found` |

The audit log's rows for that path named the customer in each, with actor
kind and status: 401, 403 twice, 204 by the key and 204 by the owner.

The starter built with `contrib/identity-session` as v0.10.0 shipped it, on
the follow-up's gobit, exits 1: `admin_route_unscoped … PUT
/admin/v1/customer-credentials (served by …(*Module).putCredential-fm) demands
no privilege`.

## 7. The third round: ownership instead of reach

A follow-up review found that judging a route by what its pattern could reach
refused a storefront's own routes at the root: `/{category}/{product}`,
`/{lang}/{category}/{slug}`, a single-page app on `/*`, and
`/{year:[0-9]{4}}/{slug}`, whose regular expression can never match `admin`.
It also found two shapes that still answered: a router of a module's own type
embedding a chi router, mounted at `/{tenant}`, whose not-found handler gave a
`product:read` key 204 on a PUT to `/admin/v1/x`; and a percent-encoded prefix
(`/admin%2Fv1/x`, `/%61dmin/v1/x`), which passed the gate.

chi matches a static segment before a parameter or a catch-all and does not
leave a static subtree that ends in a catch-all. With gobit's own routes on
`/admin/v1`, `/admin/v1/*` and `/admin/ui/*`, and on every method the panel
does not take at `/admin/ui`, the reviewer's probe and this round's tests saw
none of the public shapes answer `/admin/v1`, `/admin/v1/`, `/admin/v1/x` or
`/admin/v1/a/b`, while each kept its own paths and a guarded admin route still
answered 403.

Against the second round's code:

```
TestAPublicRouterStartsAndAnswersNoAdminPath       Received unexpected error (the gate refused it)
TestARouterThatIsNotChisOwnTypeAnswersNoAdminPath  expected: 404 actual: 204; Should be zero, but was 1
TestAPercentEncodedPatternIsRefused                An error is expected but got nil
TestTheAdminPrefixIsGobitsAlone                    An error is expected but got nil
```

`TestAModulesNotFoundDoesNotAnswerAnAdminPath` and
`TestAModulesMethodNotAllowedDoesNotAnswerAnAdminPath` passed before and after:
the second round's takeover and this round's ownership give the same answers.
A router mounted under a prefix still has its fallbacks replaced, because the
prefix's catch-all is not consulted inside it; the root's are left to the
module, since no admin path reaches them.
