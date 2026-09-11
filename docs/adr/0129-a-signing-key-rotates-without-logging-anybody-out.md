# ADR 0129 — A signing key rotates without logging anybody out

**Summary:** `contrib/identity-session` accepts a list of retired keys it never
signs with, so changing the signing key leaves every session in flight working.
It costs one field and two startup refusals, and buys a key that can actually be
changed.

- **Status:** Accepted
- **Date:** 2026-09-11

## Context

The module signed and verified with one key. Changing it made every cookie in
every browser stop verifying at the same instant, so a rotation cost every
shopper their session — which is why keys do not get rotated, not a reason they
should not be.

Its own package documentation said so: "It does not rotate the signing secret."
A limitation written down is a limitation somebody can close, and this record
closes that one.

## Decision

`identitysession.Options.RetiredSecrets` holds keys a cookie may still carry and that nothing
signs with. A rotation is: move the current key there, put a new one in
`identitysession.Options.Secret`, deploy.

## Consequences

Signing always uses the current key and verification tries it first and then each
retired one. That ordering is the whole feature: an implementation that accepted
both and went on signing with the old key would pass every test about sessions
still working and would have rotated nothing.

A retired key stays useful for one session lifetime, so it is dropped from the
list one TTL after the rotation. Nothing enforces that, and nothing can — only
the operator knows when the last cookie signed with it expired. What the record
says instead is what a list that grows forever is: a slowly widening set of keys
that can mint a session.

A LEAKED key is not retired. Retiring it keeps it able to mint sessions; it is
dropped outright, which does log everybody out and is the correct price. That is
the one case where the old behaviour is the right one.

Two things are refused at startup. A retired key shorter than the floor, because
it still accepts everything it signed and the likeliest way a short one gets in
is a placeholder somebody left while working the rotation out. And the current
key listed as retired, which reads as a rotation and is not one.

Sealed values follow the same keys as sessions. A ceremony token outliving a
rotation matters less — it expires in minutes — but a verifier that rotated one
path and not the other would be two answers to "which keys does this installation
accept", and the second would be found by somebody's failed passkey sign-in.

The verify loop stops at the first match, which leaks WHICH of an installation's
own keys signed a cookie the caller already holds. That is nothing usable, and
each comparison is still constant time — which is the half that is about the
MAC's bytes rather than about which key made them.

Writing this record also widened the documentation gate. It could not resolve a
symbol in a contrib tree and said nothing about it, which a deliberately wrong
name proved; its package walk now includes them, and the first run found a godoc
link this repository shipped two records ago.

Measurement: [measurements/0129](../measurements/0129-what-a-rotation-has-to-not-break.md)

## Rejected

**Re-signing a cookie carried by a retired key on the way past.** It would empty
the list by itself, and it makes every verification a write to the response — on
twelve storefront routes, for a saving the operator can have by waiting one TTL.

**A key id in the cookie, so only one MAC is computed.** It names which key
signed a session to anybody who can read a cookie, to save one hash on a path
that already does one.

**Keeping the single key and accepting the logout.** It is the state that made
rotation something nobody does, which is how a key stays in place for years.
