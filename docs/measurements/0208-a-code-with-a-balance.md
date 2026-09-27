# A code with a balance — measured 2026-09-27

The evidence behind [ADR 0208](../adr/0208-a-gift-card-is-a-code-with-a-balance.md).

## 1. What a gift card could stand on

| Part | State |
|---|---|
| `is_giftcard` | stored, exported, imported and exposed on the product; read by no cart, order, checkout or payment code |
| the balance tenders | store credit and loyalty points run one machine (`balancetender`), each with its own ledger and session table |
| a tender's owner | the collection's customer, never the payment's data, which is the client's (ADR 0152) |
| where a person-bound tender is registered | only where the customer claim is proven (`PersonBoundTenders`) |
| the checkout's early refusal (ADR 0175) | `CheckTender(provider, customer)`: nothing of the payment's data reached it |
| the payment's data | handed to the provider as it came; the module's session keeps what the provider returns, and the checkout's plan does not write it to the workflow record |
| the checkout | one `payment_provider_id` per completion; a collection can hold several sessions (ADR 0118), the checkout opens one |
| the journal | a capture through a module tender debits that tender's account; every other provider's, its clearing |

## 2. The code

Ten random bytes from `crypto/rand`, in the identifiers' Crockford alphabet:
sixteen characters, 80 bits. Against a shop holding a million cards, a guesser
making a million attempts a second finds one card in about 38,300 years
(2^80 / 10^6 / 10^6 seconds).

The shop keeps the SHA-256 of the normalized code in hex, which the schema
checks as 64 hexadecimal characters, and the last four characters.

## 3. On the production wiring

`TestAGuestPaysWithAGiftCard` issues a 50,000 TRY card through the admin API,
completes a guest's cart with `gift_card` and the code, and reads the card
back: the order is placed and the card holds 50,000 less the cart's total. The
code was then searched for in `payment_sessions.data`,
`workflow_executions.input`, `workflow_executions.output` and
`workflow_execution_steps.output`, and found in none.

`TestAGiftCardThatCannotPayOpensNoOrder` completes two carts that cannot be
paid: a code nobody was given (422 `payment_gift_card_unknown`) and a card in
euros for a lira cart (409 `payment_gift_card_currency`). No order was opened
for either.

## 4. On the real schema

`TestAGiftCardIsSpentOnTheRealSchema`: a card of 50,000, a guest collection of
20,000 held (30,000 left), captured, and 5,000 refunded (35,000 left). The
card's history holds three rows, the issue, the hold and the refund; the
capture writes none. The journal books the issue Dr `gift_card_granted` /
Cr `gift_card`, the capture Dr `gift_card` / Cr `receivable`, and the refund
the reverse.

`TestAGiftCardAuthorizationWaitsOnTheCard` forces the interleaving the balance
tenders' lock test forces: a competitor holds the card's lock, the authorization
is seen waiting on it through `pg_blocking_pids`, the competitor spends the
whole balance and commits, and the authorization declines. It passes at the
server's default isolation and on a pool defaulting to repeatable read.

`TestTheGiftCardConstraintsAreTheLastDefence` writes past the service in raw SQL
and names each refusing constraint: a digest that is not one, a tail that is not
four characters, a blank reason, a hold written positive, a hold with no
session, an issue with one, a second issue, a kind outside the vocabulary, and a
digest taken twice.

`TestAGiftCardHoldsBackTheRollback`: rolling 000009 back goes through on a
database with no card and stops on one holding a card, which is still there.

## 5. Two gates that counted by hand (D145)

The payment ledgers' door gate (`paymentLedgerChokePoints`) and append-only
gate (`paymentLedgerTables`) named their ledgers in lists. With the gift card's
ledger, queries and tender written and neither list touched, both stayed green:
neither read the new ledger. `TestEveryPaymentLedgerHasADoor` now derives every
`Insert…Entry` from the generated queries, and `TestTheLedgerTableListIsTheSchemas`
every `payment_…_entries` table from the migrations; each fails when its list
lacks one.

## 6. Mutations

| # | Mutation | Killed by |
|---|---|---|
| G1 | a card pays in any currency | the provider's test, the end-to-end test |
| G2 | a malformed code told apart from an unissued one | the provider's test |
| G3 | the card's lock taken nowhere | the lock test, both isolation levels |
| G4 | the issue outside one transaction | the service's test |
| G5 | the issue row not written | the service's tests, the module's integration tests |
| G6 | the digest of the printed code kept | the service's test, the end-to-end tests |
| G7 | a card's capture booked to a clearing | the chart test, the module's integration test |
| G8 | the issues not read into the journal | the module's integration test |
| G9 | the early refusal sent no data | the service's test, the end-to-end tests |
| G10 | the gift card not registered | the module's registration tests, the end-to-end tests |
| G11 | the issue's answer without its code | the API's test, the end-to-end tests |
| G12 | the gift card ledger without a door | the derived door gate |
| G13 | the gift card ledger not listed | the derived table gate |
| G14 | the rollback not stopping | the rollback test |
| G15 | a hold without its session | the constraint test |
| G16 | a card issued twice | the constraint test |
| G17 | O not read as zero | the code test |
| G18 | a code of 40 bits | the code test |

G5 and G10 first failed to compile, which is not a kill; both were written again
to compile and failed on assertions.
