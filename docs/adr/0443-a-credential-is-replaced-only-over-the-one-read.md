# ADR 0443 — A credential is replaced only over the one read

**Summary:** `contrib/identity-session` replaces a credential only over the address and hash its caller read (`ReplaceCredential`), writes a password and the sessions' anchor in one statement, and dates a sign-in's session at its read (`Sessions.IssueAsOf`).
It costs a second write path beside `Put` and two optional store interfaces, and buys a password check that a reset committed after its read can neither be undone by nor outlive.

- **Status:** Accepted
- **Date:** 2026-10-15
- **Amends:** [0127](0127-a-working-identity-ships-outside-the-module.md), whose store wrote only unconditionally, and [0374](0374-a-replaced-password-ends-the-sessions-before-it.md), whose anchor moved in a statement of its own before the password

Measurement: [measurements/0443](../measurements/0443-what-a-sign-in-undid.md)

## Context

An embedder that carries passwords over from another system checks the old
hash at the customer's first sign-in and writes an argon2id one with
`Credentials().Put`, which replaces whatever the customer has. A reset or an
operator's `PUT /admin/v1/customer-credentials/{customer_id}` landing between
the check and the `Put` was undone: depremtek's backend, the consumer,
reproduced the old password signing in and the new one refused (D297).
Behind that sat the module's own window. A reset moves the anchor and then
writes the password, and a sign-in that read the old hash dates its cookie
at its issue, after the anchor: the module's own sign-in kept a session
across a reset that landed during its check (D298). Binding another store
through `Options.Credentials` cannot close either: the module finds its
registration, reset and erasure records on its own store by type.

## Decision

`ReplaceCredential(ctx, customerID, expected, replacement)` writes only while
the customer's stored address and hash are `expected`, or with a zero
`expected` only for a customer with none, and the reset, the password change
and the operator's route write the password and the anchor in one statement.
The sign-in dates its session at its read with `Sessions.IssueAsOf`, which an
embedder that checks a password itself calls the same way.

## Consequences

- The module's own table implements both (`CredentialSwapper`, `AnchoredPut`); a store bound through `Options.Credentials` that implements neither keeps the two-statement order, and `ReplaceCredential` answers `ErrSwapUnsupported` for it and `ErrNotRegistered` before `Register`.
- A reset, a change or a proven address change made between a caller's read and its swap stands; the swap answers false and the caller answers as for a wrong password.
- A password replaced after a sign-in's read moves the anchor past the read, and the cookie that sign-in issued proves nobody. The anchor's moment is taken as the write starts and compared at a millisecond, so a read inside the replacement's own write, or in its millisecond, is not seen.
- A first password written through these routes moves the anchor too: an account that signed in another way (a passkey) ends those sessions when it gets one.
- `ReplaceCredential` does not move the anchor: a password re-hashed is the same password. A caller that changes a password moves it through `SessionAnchors`, which the module's store implements.
- An address another customer holds is refused as `Put` refuses it, by the unique index.

## Rejected

- A wrapping store through `Options.Credentials`: it loses the records the module finds on its own store by type.
- A lock the embedder takes around its read and `Put`: the module's routes would have to take it too.
- Moving the anchor after the password: a failure between the two leaves the old sessions standing.
- Comparing `updated_at`: the hash and the address are what the caller read.
