# The first secret that cannot be hashed

Evidence for [ADR 0143](../adr/0143-an-administrator-can-hold-a-second-factor.md).

Measured 2026-09-12.

## The starting point, counted rather than assumed

```
$ grep -rl -iE 'totp|mfa|two.factor|otpauth' --include='*.go' --include='*.md' .
internal/modules/notification/service/interop.go
```

One file, and its match is a sentence in the notification interop's godoc naming
this as future work: "a password reset and an MFA enrollment are the next two".
Nothing else in the tree — no table, no endpoint, no library.

## What every other secret in this module does, and why this one cannot

| Secret | Stored as | Can the database produce the original? |
|---|---|---|
| a user's password | argon2id | no |
| an API key | SHA-256 | no |
| an invitation token | SHA-256 | no |
| **a TOTP seed** | **AES-GCM ciphertext** | **yes, with the key** |

The first three are values the module only has to RECOGNIZE. Verifying a
six-digit code means recomputing it from the seed, so the seed has to come back.
That is what makes this the first recoverable secret the module holds and why the
storage question needed its own decision rather than following a pattern.

There was no encryption primitive to follow either:

```
$ grep -rn -iE 'aes\.|cipher\.|NewGCM|encrypt' --include='*.go' core internal | grep -v _test
(three comments about unencrypted TRAFFIC; no primitive)
```

## What the encryption buys, stated as narrowly as it is true

| Threat | Defended? |
|---|---|
| a stolen backup or a replica handed to an analyst | yes |
| an SQL injection that can only SELECT | yes |
| an attacker who owns the process | **no** — it can decrypt |
| an attacker who owns the host | **no** — the key is in its environment |

Half a defense written down as half a defense. The alternative shapes were a key
derived from `JWT_SECRET` (rejected: rotating the signing secret would stop every
enrolled authenticator) and plaintext with a line in `known-limits.md` (rejected:
the feature's whole value is that a database read is not enough).

## RFC 6238, executed

The implementation is checked against the standard's own Appendix B vectors — the
SHA-1 rows, which are what authenticator apps compute.

| T (seconds) | RFC's 8-digit code | last six | what this module produced |
|---|---|---|---|
| 59 | 94287082 | 287082 | 287082 |
| 1111111109 | 07081804 | 081804 | 081804 |
| 1111111111 | 14050471 | 050471 | 050471 |
| 1234567890 | 89005924 | 005924 | 005924 |
| 2000000000 | 69279037 | 279037 | 279037 |

All five on the first run. The fourth row is the one worth keeping: `005924`
begins with two zeros, and an implementation formatting the number without
padding would answer `5924` — which no app shows and no person can type.

## Why SHA-1, measured against the apps rather than the RFC

RFC 6238 allows SHA-1, SHA-256 and SHA-512 and makes SHA-1 the default. What
decides it here is that the `algorithm` parameter of an `otpauth://` URI is widely
ignored by authenticator apps: an app that ignores it computes SHA-1 whatever the
URI said, so choosing SHA-256 would produce enrollments that scan, display a code,
and never verify.

The property SHA-1 lost is collision resistance. HMAC does not rest on it, and
HMAC-SHA1 has no practical attack. That sentence is the gosec suppression on the
import.

## The mutations

| # | Mutation | Result |
|---|---|---|
| 21 | the guard that refuses enrollment without a key | **survived**, then bit |
| 22 | `HasConfirmedMFA` always answers true | bit (3 tests) |
| 23 | the acceptance window widened from ±1 step to ±1000 | bit (1 test) |
| 24 | an API key allowed to enroll | bit (1 test) |

**Mutation 21 is the one worth the space.** Removing the guard changed no outcome:
the cipher refuses to seal on its own, with the same error kind, so every
assertion still passed. That is the shape ADR 0142 deleted a guard for — a check
nothing can break.

Here it was KEPT, and the difference is the reason. The cipher's message is
"nothing can be sealed"; the guard's names `MFA_SECRET_KEY`. An operator reads one
of those and knows what to do. So the guard's product is the MESSAGE, the test now
asserts the variable name, and the mutation bites.

## Two more mutations, and the one nothing can catch

| # | Mutation | Result |
|---|---|---|
| 25 | the negative-clock guard deleted | **survived** — and the guard was deleted |
| 26 | `hmac.Equal` replaced with `==` | **survived**, and no test can catch it |

Mutation 25 was a guard, not a bug: a clock before 1970 gives a negative step, and
the `at < 0` check inside the loop already skips every candidate. Two checks for
one case, so the outer one went — the same call ADR 0142 made.

Mutation 26 is the honest one. Timing is not observable from a unit test, and a
test that measured it would be the flakiest in the suite. Counted while writing
this: the tree holds FIVE constant-time comparisons — a TOTP code, an argon2id
derived key, two cookie MACs and a token digest — and nothing refuses `==` in any
of them. That is now written into `known-limits.md` under the limit of the
invariants, together with what actually defends the six-digit case: the shape of
the code and the rate limit in front of the endpoint.

## A method error, recorded because it hid three results

The first mutation run reported three survivors. Two of them had not compiled:
`return true, nil` left a variable unused, and the runner counted `--- FAIL`
lines, which a build failure does not produce. A compile failure is not a bite,
and a counter that cannot tell the two apart reports the opposite of the truth.

The re-run used `x || true` and `&& false` forms that compile, and separated
"build failed" from "test failed" in the runner itself.

## What was not measured

Whether requiring a second factor at login is safe to add. It is not part of this
record: recovery codes, what an API key does when its owner has a factor, and what
somebody locked out of their own phone does are three questions with no answers
yet, and none of them can be answered before people can enroll.
