# What a shop could not hold

Evidence for [ADR 0152](../adr/0152-a-shop-can-hold-money-for-a-customer.md).

Measured 2026-09-12, while choosing the next item from the feature list's A
section.

## Nothing in the tree held a balance

The feature list's A7.6 row says "nothing at all, and no ADR closes the
question". Measured against the tree rather than taken on trust:

```
$ git grep -il 'store_credit\|customer_balance' HEAD -- '*.sql'
(no match; the one hit for gift_card is a SHIPPING PROFILE type)
```

The payment module carried three migrations — `payment_init`,
`payment_reconcile_index`, `money_records_are_never_deleted` — and none of them
names a customer. So the row is right, and the interesting question was not
whether the ledger existed but what stopped it from being one table.

## What stopped it: a collection named nobody

`CreateSessionInput` on the published provider contract carried `Amount`,
`CurrencyCode`, `Reference`, `IdempotencyKey` and `Data`. `Reference` is the
caller's own identifier and this module deliberately does not validate it
(Principle 2.2). `Data` is what the CLIENT sent.

So a provider whose funds belong to a person had exactly one place to learn whose
money it was spending: `Data`. That is the client's own map, which makes "spend
that other customer's balance" a request anybody can write.

The field had to be added where the SERVER knows the answer, and the server knows
it at the collection: the checkout's plan is holding the cart, and the cart's
customer has been PROVEN since ADR 0125. Hence a column on `payment_collections`
and a field on the contract, rather than a convention inside `Data`.

## Three columns that had to be declared

`TestNoStorefrontWriteStoresAnUnjudgedIdentityClaim` (ADR 0051) flagged all three
new `customer_id` columns as soon as they appeared. They are recorded CONFINED
rather than exempted, and the argument is the same for each: the writer is either
an admin act behind `payment:write` or the checkout's own plan, and no storefront
body reaches them.

The one installation where that argument fails is the one that has gone back to
`STOREFRONT_TRUST_UNVERIFIED_CUSTOMER_CLAIM`. There the cart's customer is NOT
proven, so the confinement is a claim about a shape the configuration has
changed. The answer is not a fourth limb: the provider is not registered at all
in that installation, which makes the dangerous combination unreachable rather
than documented.

## The lock, and a test that could not see it

The provider reads the balance and acts on it, so the correctness argument is
that two authorizations for one customer cannot both see the same money. The
first test written for it ran eight goroutines over eight sessions against a
balance covering one, and asserted that exactly one passed.

It passed with `LockStoreCreditEntries` REMOVED.

The reason is the fixture, not the claim: against a local server each
transaction finished before the next goroutine reached its first statement, so
the interleaving the test was written to produce never happened. This is the
"fixture that cannot discriminate" class, and it is worse here than usual — the
test's name says the money cannot be spent twice, and a reader would have taken
it as proof.

The replacement produces the interleaving instead of hoping for it, in the shape
`internal/modules/auth/repository/locking_integration_test.go` already uses: a
competing transaction takes the ledger's rows, the authorization under test is
OBSERVED to wait on that specific backend through `pg_blocking_pids`, the
competitor then spends the balance and commits, and only then does the
authorization proceed — and declines, because it re-reads the balance it was made
to wait for.

With the lock removed, nothing waits and the assertion fails in ten seconds.

## The mutations

| # | Mutation | Result |
|---|---|---|
| 76 | the hold is written POSITIVE | **bit** (2) |
| 77 | the ledger is not locked before it is read | **bit** (1), and see above |
| 78 | capture takes the money a second time | **bit** (3) |
| 79 | an insufficient balance returns an error, not a decline | **bit** (1) |
| 80 | an empty reason is accepted | **bit** (1) |
| 81 | the currency is not normalized | **bit** (1) |
| 82 | a session with no customer is accepted | **bit** (2) |
| 83 | the balance endpoint echoes the raw currency | **bit** (1) |
| 84 | the balance endpoint stops writing the unit note | **bit** (1) |
| 85 | the checkout stops passing the customer | **survived** the unit lane |
| 86 | the module registers the provider whatever the option says | **bit** (1) |
| 87 | the option is ignored the other way | **bit** (1) |
| 88 | a refund is written negative | **bit** (1) |
| 89 | the admin endpoint drops the reason | **bit** (1) |

Mutation 85 is the one worth the space. Replacing `s.plan.CustomerID` with `""`
left every test in `internal/workflows/checkout` green and broke only the
end-to-end proof. The field is read by nothing in that package — it is copied
from the plan into a call — so the saga's own suite had no reason to look at it,
and the tender would have stopped working for everybody the day somebody
refactored the step. Its witness is a test whose subject is what the CONSUMER
receives: `stubPayments.lastCollectionCustomer`.

Mutation 84 is the other half of a gate that was measured too narrow. The
module's schema test requires every amount-carrying endpoint to write the minor
unit note, and derives the population from the schema rather than from a list —
correctly — but `tutarAlani` matched `amount` and `*_amount` only. A balance is
an amount, in minor units, and the balance endpoint was invisible to the rule its
own sentence states. Widened, then proven with the mutation above.

## What is NOT closed

- **A customer cannot see their own balance.** There is no storefront endpoint,
  because reading one needs the customer claim in the request PROVEN, and that
  proof is a surface this module is not wired to. An operator reads it for them.
- **There is no expiry.** A credit issued today is spendable forever; expiring
  one means a scheduled job writing negative rows, and what it must not do is
  race a checkout that is holding the same money.
- **Nothing ties a credit to what caused it.** `reference` is free text, so
  "this 200 lira is the compensation for return R-19" is a convention rather
  than a link.
- **The legacy flag removes the tender rather than securing it.** A shop that
  needs both the flag and store credit has no answer here.

## What was not measured

Whether a shop would rather have gift cards — a bearer instrument, spendable by
whoever holds the code — than customer-bound credit. They are different
decisions: a gift card has no owner until it is redeemed, so none of the
confinement argument above applies to it.
