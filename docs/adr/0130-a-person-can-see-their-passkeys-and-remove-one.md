# ADR 0130 — A person can see their passkeys and remove one

**Summary:** `contrib/identity-passkey` lists the caller's own keys and removes
one, refusing the removal that would leave an account with no way in. It costs a
row lock and a seam to whatever else signs somebody in, and buys a person the
ability to revoke a device they no longer hold.

- **Status:** Accepted
- **Date:** 2026-09-11

## Context

The module could register a passkey and sign in with one, and nothing else. A
person whose phone was lost or sold could neither see what still opens their
account nor revoke it, and every "register another passkey first" sentence the
module writes assumes they can tell what they already have.

Removal is where the hazard is. For an account with no password the last passkey
IS the account, so removing it is a lockout with no recovery path, performed by
the owner, reported as a success. Two of a person's devices doing it at the same
moment is worse — each reads "there are two, removing one is fine" and both are
right about a world that is already gone.

## Decision

`GET /store/v1/auth/passkey/keys` answers the caller's own keys with an advisory
`removable` flag, and `DELETE /store/v1/auth/passkey/keys/{credential_id}`
removes one and refuses the last way in. Whether another way in exists is this
module's OWN question, asked through an `OtherSignIn` seam that defaults to the
bound session module's password lookup.

## Consequences

The guard is a LOCK, not a condition. `SELECT … WHERE customer_id = $1 FOR
UPDATE` runs in the same transaction as the DELETE, because under READ COMMITTED
the same guard written inside the DELETE leaves two concurrent removals with zero
keys — measured here, every run.

The cross-module question is asked BEFORE that transaction opens. A query made
while holding a row lock takes a second connection from the same pool, and enough
concurrent removals would each hold one and wait for another. The unlocked count
it decides on can be stale and both directions are safe: the locked count refuses
on its own whenever more than one row is held.

"We could not check" is a 500 and never a 409, which is why
`identitysession.ErrPasswordUnknown` is a named error. Telling somebody their
account has one door when nobody looked is worse than failing. The wire cannot
carry the distinction — `core/http` masks every internal message on purpose — so
it lives in the log, and the tests assert it there.

The seam asks "is there another way in", never "does this customer have a
password": an installation may bind a verifier that is not the session module.

Every miss is one code: never existed, already gone, somebody else's, and not the
canonical spelling — which is now the only accepted one (gap D67). A credential id
is not secret, since the authenticator hands it to every relying party, so telling
the misses apart would answer whether any id a caller cares to try belongs to
somebody.

NOT closed, and named rather than papered over: a stolen session cookie can
register its own key and remove the owner's. The rule defended here is "an
account keeps a way in", not "only the owner changes credentials", and a gate
that looked like the second while enforcing the first would be worse than none.

Measurement: [measurements/0130](../measurements/0130-the-last-way-in.md)

## Rejected

**A third method on `Credentials`.** That interface exists so an installation can
bind LDAP or an existing users table; the lookup is an optional capability.

**`pg_advisory_xact_lock`.** Identical behaviour, and it costs a lock CLASS from a
registry that lives in the main module — a contrib module coordinating across a
module boundary for what a row lock already does locally.

**Trusting the listing's `removable` flag.** It is advisory by construction: the
answer is a moment old and the removal decides again under the lock.

**Asking the seam inside the transaction.** It is the pool-exhaustion deadlock,
and it buys a fresher answer that the locked count overrules anyway.
