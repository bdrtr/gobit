# ADR 0382 — A passkey whose counter does not advance is suspended

**Summary:** A passkey sign-in records the signature counter its key reports,
and a device-bound key whose counter does not advance signs nobody in again.

- **Status:** Accepted
- **Date:** 2026-10-04

## Context

A device-bound passkey counts its signatures, and a copy of its private key
counts on its own, so the one sign of a copy is an assertion whose counter does
not exceed the one recorded. go-webauthn compares the two, returns the
credential with the new count or a clone warning, leaves the policy to the
relying party, and asks for the counter, the backup state and the user-verified
flag to be written back after every sign-in. `contrib/identity-passkey` dropped
the returned credential and stamped `last_used_at` alone, so every stored
counter stayed at its registration value and the warning was never read (D232).
A copied private key signs whatever counter it is given, so a copy can start
ahead of the owner, and refusing the one sign-in that did not advance refuses
the owner.

Measurement: [measurements/0382](../measurements/0382-a-copied-passkey.md)

## Decision

A sign-in is recorded through `Credentials.SignedIn`, which compares the
assertion's counter with the stored one under a lock on the key's row, and the
session is issued only after it has recorded the count, the backup state, the
latched user verification and the moment. A device-bound key whose counter does
not advance, other than the same count sent again within two ceremony
lifetimes, is suspended: that sign-in and every later one answer 403
`identity_passkey_key_suspended` until the key is removed.

## Consequences

- `SignedIn(ctx, Assertion)` replaces `Used(ctx, credentialID)`, so a store an
  installation binds stops compiling until it keeps the counter.
- The sign-in that suspends a key tells the account once, through
  `KeyNotices.SendPasskeySuspended`, which extends ADR 0381's seam; a messenger
  an installation binds stops compiling until it implements it.
- A copy that signs in ahead of the owner keeps its access until a sign-in
  whose count does not advance suspends the key. Sessions it already holds last
  until they expire: this module cannot end a session.
- The same count within two ceremony lifetimes is refused with
  `identity_passkey_refused` and suspends nothing, whether a finish sent twice
  or a copy that landed on it. Two sign-ins of one counting key that finish in
  reverse order suspend it.
- A backup-eligible key is never refused over its count, because it lives on
  several devices by design; its stored count only moves up, and a copy of it
  is not detected. A key that reports zero is not compared either, and a finish
  of either kind sent twice within `CeremonyTTL` signs in twice.
- A suspended key is listed with `suspended_at`, also when another way in cannot
  be checked, is always removable and is not a way into the account (amends ADR
  0130). A person whose only way in it was cannot sign in to remove it; the shop
  removes it through `Store()`.
- A sign-in whose record cannot be written answers 500, so `last_used_at` no
  longer lags; a key removed or replaced during the ceremony signs nobody in.
- The handler refuses an assertion the library flagged whatever the store
  answered, so a store that records it anyway still turns a copy away.
- The count stays in the credential's JSON, where the library reads it;
  `suspended_at` (migration 000003) is declared as personal data.

## Rejected

- Signing in and logging the warning: a signal that stops nothing.
- Refusing the sign-in alone: a copy that starts ahead keeps signing in, and the
  refusal lands on the owner.
- Deleting the key: the person loses the row that says what happened, and its
  id can be registered again.
- An optional capability beside `Credentials`: a store that does not offer it
  keeps, silently, the fault this record closes.
- Writing the returned credential back whole: it would store a clone warning
  nothing clears, and a stale read would undo a latch another sign-in set.
- A column for the counter: a second copy of a value the library reads from the
  JSON.
- Recording each ceremony's challenge to tell a repeat from a copy exactly: a
  column for a spent nonce, against a window that refuses rather than suspends.
- Ending the account's sessions on suspension: this module cannot reach the
  session anchor, and a passkey-only account has none.
