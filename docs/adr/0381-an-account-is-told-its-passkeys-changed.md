# ADR 0381 — An account is told its passkeys changed

**Summary:** `contrib/identity-passkey` tells an installation, by customer id
and key id, that a passkey was added to an account or removed from it, through
an optional `KeyNotices` seam; the installation finds the address.

- **Status:** Accepted
- **Date:** 2026-10-04

## Context

Since ADR 0130 a signed-in cookie registers a passkey with
`POST /store/v1/auth/passkey/register/finish` and removes one with
`DELETE /store/v1/auth/passkey/keys/{credential_id}`, and nothing more is
asked. A stolen cookie can leave a key of its own, which signs in after the
cookie expires and after the password is replaced, and take the owner's away.
ADR 0379 told an account of a replaced password and a moved address; a key
added or removed told nobody. This module knows an account by the customer id
its identity proves, and keeps no address.

## Decision

The module calls `KeyNotices.SendPasskeyAdded` after a registration stores a
key the account did not hold, and `KeyNotices.SendPasskeyRemoved` after a
removal is written, each with the customer id and the key's id as the listing
shows it. A notice is sent after the write on a context the caller cannot
cancel and that ends after the session module's `DefaultNoticeTimeout`, nil
sends nothing, and one that cannot be sent is logged rather than failing the
request.

## Consequences

- The installation turns the customer id into an address from its own record,
  so nothing in the request chooses who is told.
- A refused or failed registration or removal tells nobody, and neither does a
  key the account already holds sent again, which writes nothing (D229), so a
  client retrying an answer it lost is not told twice.
- A notice that cannot be sent is logged at WARN with the customer id and the
  key id, so an operator can reach the person another way.
- A seam holding a nil pointer counts as nil, decided once when the module is
  built.
- A key written or removed through `Module.Store`, or by an erasure, tells
  nobody, and neither does a sign-in: the seam is the two endpoints'.
- Telling is not stopping. The cookie stays good until it expires, ADR 0130's
  limit stands, and an account with no password in the session module cannot
  end its other sessions (ADR 0374).
- Its method names differ from `AccountNotices`'s, so one messenger implements
  both. `examples/starter` binds no passkey module (ADR 0128) and implements no
  stand-in.

## Rejected

- Widening the session module's `AccountNotices`: it is keyed by an address
  this module does not have.
- The address through the session module's credential row: an account that
  signs in with passkeys alone has none, and it is the account a stolen key
  ends.
- The customer id alone: a removed key is gone from the listing, and the
  notice is the one place left that names it.
- Failing the request when the notice fails: the change has been made, and an
  error would say it had not.
- Refusing to start without the seam: nil is today's behavior, as the session
  module's notices are optional.
