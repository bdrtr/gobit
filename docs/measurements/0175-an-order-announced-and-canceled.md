# An order announced and canceled — measured 2026-09-25

The evidence behind [ADR 0175](../adr/0175-a-payment-that-can-never-be-made-opens-no-order.md).

## 1. What the server did for a guest's tender

Measured while driving the storefront example (ADR 0174): a guest cart with one
line and a delivery, completed with `loyalty_points`. The server log, in order:

```
msg="order placed" workflow=complete_cart … amount=43300
msg="notification NOT SENT: the 'log' provider only records" … template=order.placed
msg="workflow: a step failed, compensation is starting" … step=authorize_payment step_index=3
    error="payment_loyalty_points_no_customer: …"
msg="compensation: order canceled"
msg="compensation: stock reservations released"
status=409
```

The installation had no mail provider, so the notification was only logged.
With one, the guest is mailed that the order was placed.

## 2. The same for every refusal known in advance

`TestAPaymentTheCartCannotMakeOpensNoOrder` (`internal/e2e`) was written before
the fix and run against the tree as it stood. Every case got the status and
code it gets today, and every case opened an order:

```
--- FAIL: …/a_guest_spending_loyalty_points   Should be zero, but was 1
--- FAIL: …/a_guest_spending_store_credit     Should be zero, but was 1
--- FAIL: …/a_provider_nobody_registered      Should be zero, but was 1
```

The third case is a typo in a provider id. It reaches the payment step for the
same reason: `prepare` refreshed the totals, compared the revision and
`expected_total`, and never asked about the provider.

## 3. Where the refusals were decided

| Refusal | Decided in | Depends on |
|---|---|---|
| provider not registered | `ProviderRegistry.Get` | the registry, fixed at startup |
| a person's balance for a guest | `balancetender.Machine.CreateSession` | the cart's customer |
| balance too small | `Machine.Authorize` | the ledger at that moment |
| card declined | the provider | the provider at that moment |

The first two are the ones the checkout can ask ahead of the order. The other
two stay at the payment step.

## 4. The gates, and what bites them

| Mutation | End-to-end | Unit |
|---|---|---|
| `CheckTender` ignores the owner rule | 2 of 3 red: both tenders | 3 of 7 red: the guest cases |
| `prepare` does not ask | 3 of 3 red | the checkout's refusal test red |

The existing `TestAGuestChoosingPointsGetsAConflictOverHTTP` stayed green through
the change: it asserts the 409 and the code, which were right before, and not
whether an order was opened.

## 5. The language ledger

The registry's not-found message used to stay inside the saga's wrap. Now it is
returned to the client. `internal/modules/payment/service/registry.go` and its
test were translated, and the ledger went from 186 files to 184.
