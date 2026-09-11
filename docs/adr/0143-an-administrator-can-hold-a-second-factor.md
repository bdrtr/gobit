# ADR 0143 — An administrator can hold a second factor

**Summary:** An administrator enrolls a TOTP authenticator for THEMSELVES and
proves it with one code; the secret is encrypted with a key the installation
supplies and enrollment is refused without one. It costs a variable an operator
must set, and it does not yet change how anybody signs in.

- **Status:** Accepted
- **Date:** 2026-09-12

## Context

Signing in is one step: an e-mail and a password, or an invitation the person
turned into a password (ADR 0137). A stolen password is the whole account, and the
account is the admin surface.

The storefront already has a second shape — a passkey is something the person HAS
(ADR 0128) — and the admin side has nothing. What it needs first is not
enforcement but the ability to HOLD a factor: requiring one before anybody can
enroll locks out the installation that turned it on.

Measurement: [measurements/0143](../measurements/0143-the-first-secret-that-cannot-be-hashed.md)

## Decision

`auth_mfa_credential` holds one TOTP secret per user, encrypted with
`MFA_SECRET_KEY`, and two endpoints under `/admin/v1/auth/mfa` let the CALLER
enroll and confirm. A credential counts only after its first correct code. Nothing
in the login path reads it yet.

## Consequences

This is the first secret this module stores that it can read back. A password is
argon2id, an API key and an invitation token are SHA-256, and none of them can be
recovered — verifying a six-digit code means recomputing it, which means holding
what the phone holds. So it is sealed with AES-GCM, and what that buys is exact:
it defends a database read WITHOUT the process — a backup, a replica, an
injection that can select — and not a compromised host.

Without a key, enrollment is REFUSED: a default key is no key, and plaintext is a
security feature quietly doing less than its name. The key is separate from
`JWT_SECRET` because the two rotate on different clocks — rotating a signing
secret costs a round of sessions, rotating this one would stop every authenticator
already enrolled.

The endpoints name no user, and the address says so. An administrator who could
enroll a factor for a colleague would hold the secret of their phone, which is the
whole of what the factor is worth — the reasoning that keeps an invitation token
out of its issuer's response. An API KEY is refused for the mirror reason: a
machine holds no authenticator, so its secret has no reader but the database.

TOTP is written out rather than taken as a dependency: thirty lines whose
specification is four pages, against a library in every embedder's module graph —
and the RFC publishes test vectors, so it is checked against the standard rather
than against itself. SHA-1 is a choice, not an inheritance: apps widely ignore the
URI's `algorithm` parameter, so SHA-256 would produce enrollments that look right
and never verify. HMAC does not rest on the property SHA-1 lost.

One of four mutations survived: removing the guard that names `MFA_SECRET_KEY`
changed no outcome, because the cipher refuses on its own. It was KEPT — what it
produces is the message, and "nothing can be sealed" tells an operator nothing
they can act on — and the test now asserts the variable name.

Not closed, deliberately: nothing REQUIRES a second factor. `HasConfirmedMFA`
exists and nothing calls it. Requiring one is a separate decision with its own
questions — recovery codes, what happens to an API key, and what an administrator
locked out of their own phone does — and none of them can be answered before
people can enroll.

## Rejected

- **Derive the key from `JWT_SECRET`.** It costs no configuration and makes a
  signing-secret rotation lock everybody out of their authenticator.
- **Store the secret in plaintext and write it down as a limit.** The feature's
  whole value is that a database read is not enough, and a limit does not restore
  it.
- **A TOTP library.** A module in every embedder's graph for code the RFC both
  specifies and supplies test vectors for.
- **Let an administrator enroll for a colleague.** It leaves the account with one
  factor and one more holder of it.
