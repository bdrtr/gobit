# What a wrong password took — measured 2026-10-15

Evidence for [ADR 0442](../adr/0442-an-unknown-address-signs-in-as-slowly-as-a-known-one.md)
and gap D296. The numbers are gaps on a developer laptop, not benchmarks.

## 1. Sign-in, through a consumer

depremtek's backend (`deprem-market-gobit`) brings gobit up in-process with
`contrib/identity-session` bound, behind a real `httptest` server. A
throwaway test there opens a customer, gives it a password through
`PUT /admin/v1/customer-credentials/{customer_id}`, mints a publishable key,
and posts fifteen wrong passwords to `POST /store/v1/auth/sign-in` for the
known address and fifteen for an address with no account. Every answer was
`401 identity_session_rejected`.

| tree | unknown | known, wrong password |
|---|---|---|
| v0.13.1 | 398.8 µs | 13.40 ms |
| the fix, run 1 (a `replace` in the consumer's go.mod) | 14.32 ms | 14.79 ms |
| the fix, run 2 | 13.09 ms | 13.83 ms |

## 2. Every route that answers by address, in the module's integration lane

The first draft's review measured each route against `postgres:16-alpine`
with every seam bound and a mailer that costs nothing, median of 31 requests
per address kind (`inceleme-0442/review0442_integration_test.go`, run here
with the swap tests left out, which predate the amended signature).

Before the amendment (the review's runs): the reset request answered an
unknown address in 45–64 µs and a known one in 169 µs.

After, the same probe, two runs:

| route | unknown | known |
|---|---|---|
| sign-in | 12.39 ms / 12.30 ms | 11.86 ms / 11.95 ms |
| reset request, back to back | 48.1 µs / 45.6 µs | 106.1 µs / 119.3 µs |
| reset request, 20 ms between requests | 319.8 µs / 315.2 µs | 369.6 µs / 312.1 µs |
| registration | 11.92 ms | 12.29 ms |
| address change (free / another's) | 12.90 ms | 12.17 ms |

Back to back, each known reset's recording and mail run after its answer and
share the connection pool with the next request, which the next known
request pays. With that work settled between requests, the two answers take
one time. A row written through `Credentials().Put` at argon2id `t=3`
answered a wrong password in 29.8 ms and 31.0 ms, its own cost, against the
module's 12 ms.

## 3. Mutants

`TestAnUnknownAddressIsCheckedLikeAKnownOne` counts the checks, and
`TestTheDecoyCostsWhatAStoredHashCosts` pins the decoy's parameters to
`HashPassword`'s: dropping the decoy's check, leaving the decoy empty and a
decoy at cheaper parameters fail them. Drawing the decoy from a constant
secret passes and is equivalent: the salt is still random, and an unknown
address is refused whatever the check says.
