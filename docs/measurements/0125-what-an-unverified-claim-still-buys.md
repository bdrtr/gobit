# What an unverified claim still buys — measured 2026-09-10

Serves [ADR 0125](../adr/0125-serving-an-unverified-customer-claim-is-a-choice.md).

## The surface, and the two lines that decided it

Twelve storefront routes name a customer. Eight refuse when nothing is bound;
four did not. The difference was two lines, written twice:

```go
if identity == nil {
    return claimed, nil
}
```

once in `internal/modules/cart/api/api.go` and once in
`internal/modules/b2b/api/api.go`. `corehttp.ProvenCustomer` — the one comparison
ADR 0057 built — already refuses a nil identity with `401 identity_not_bound`;
these two branches were what kept it from being asked.

## What the four routes hand over

With nothing bound and a caller who knows a customer identifier:

| Route | What it answers |
|---|---|
| `GET /store/v1/b2b/customers/{customer_id}/company` | that person's employer |
| `GET /store/v1/b2b/customers/{customer_id}/employee` | that person's spending limit |
| `POST /store/v1/carts` | opens a cart in their name |
| `POST /store/v1/carts/{id}` | hands a guest cart to them |

The identifier is not a secret: it travels in every order response. And the cart
half is not only a read — a cart opened for a B2B employee draws on that
employee's spending window, so the claim spends somebody else's allowance.

## The blast radius of closing it, measured before deciding

Both branches were turned into refusals and every lane run:

| Lane | What broke |
|---|---|
| `go test ./...` | 2 tests, both ADR 0057's own witnesses |
| `make test-integration` | the same 2, run again under the tag |
| `make smoke` | 1 — `TestB2BEndToEndInARealProcess` |
| everything else | nothing |

The three arch failures in those runs are the untracked feature list and predate
this work.

Two witnesses and one smoke scenario is the whole cost, and none of the three is
an accident: they are the tests ADR 0057 wrote to pin the behaviour it chose. The
smoke one is the consequence that record named out loud — "the shipped
`cmd/server`, which binds no identity, keeps its b2b storefront and its smoke
coverage".

## Why a setting rather than a plain reversal

ADR 0057 rejected refusing, and its reason was not wrong: it withdraws a working
surface from an embedder who did nothing wrong. A setting answers that reason
without keeping its consequence — the surface is one environment variable away,
so nothing is withdrawn from anyone who wants it.

What is withdrawn is getting it without deciding. The repository already made
that argument once, about a different switch, and the sentence transfers exactly:
*"The existence of the switch makes being closed a decision rather than an
accident."* Read the other way round, it is this record.

## Why the zero value is the closed one

The field is named positively — `TrustUnverifiedCustomerClaim` — so `Options{}`
means refuse. That matters beyond style: `internal/e2e` builds its own registry
mirroring the composition root, three module constructors there already pass a
zero Options, and any embedder assembling the modules by hand does the same. A
negatively-named field would have made every one of those the open answer.

## The one thing this does not fix

An implementation that reads a header and hands it back satisfies
`corehttp.Identity`, and the framework cannot tell. Binding a verifier is what
closes the four routes; binding a bad one closes nothing while looking closed.
That is a separate piece of work and it is not begun here.
