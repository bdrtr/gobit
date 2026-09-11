# ADR 0131 — A passkey belongs to one relying party

**Summary:** `contrib/identity-passkey` records the relying party id a credential
was registered under and answers only for the configured one. It costs a column and
a sentence in the store's contract, and buys back the guarantee ADR 0130 shipped —
which a domain change silently inverted.

- **Status:** Accepted
- **Date:** 2026-09-11

## Context

A passkey is scoped to an RP ID by the authenticator that minted it: a credential
created for one relying party is not offered when the browser is asked for another.
So an installation that changes `Options.RPID` — a domain move, or dropping a
subdomain — leaves every existing key unusable.

The rows stayed and nothing recorded which relying party they belonged to. ADR
0130's guard counts what the store reports, so it counted them: a person holding one
abandoned key and one new one was told they had two ways in, and removing the NEW
one was permitted. The rule written to prevent a lockout produced one.

The server had no opinion either. Measured before the column existed: a credential
row registered under `example.test` signed its owner in under `moved.test`, with a
204. A real authenticator would not offer it — but "the client will not do that" is
not a rule this module enforces.

## Decision

`passkey_credentials` gains an `rp_id` column, written at registration from
`Options.RPID`, and every read and write of the module's own store is scoped by it.
A credential of another relying party is invisible here: not listed, not counted as
a way in, and not found at sign-in.

## Consequences

The scope belongs to the STORE and the interface says so rather than carrying it.
`Credentials` is published for an installation keeping keys elsewhere, and six
signatures would each have grown a parameter fixed for the life of a store. The
contract states the rule and names what breaking it costs.

It cannot be derived instead: the stored credential carries no attestation bytes —
measured — so any store has to record the relying party separately.

`rp_id IS NULL` means a row written before the column and is read as belonging to
the configured relying party. That is what those rows were, unless the installation
had already moved, in which case nothing recorded what they were and no backfill
can invent it. An upgrade therefore logs nobody out.

A re-registration of the same credential id CLAIMS the row for the current relying
party. Leaving the old value would answer 204 for a key the configured relying
party cannot see, which is the module telling somebody they registered a passkey
when they did not.

The published listing description says abandoned keys are not shown: a key
disappearing unexplained is a silence an integrator has to guess at, and a
description is a promise (ADR 0026).

The customer index is replaced by `(customer_id, rp_id)` rather than joined by a
second one: every read is scoped by both, and a two-column index with the customer
first answers a customer-only question just as well.

Measurement: [measurements/0131](../measurements/0131-a-key-bound-to-a-domain.md)

## Rejected

**A startup scan warning the operator about abandoned rows.** The cost lands on
exactly the wrong installation: with no abandoned rows the query is a full scan of
the table, and with them it stops at the first match. A healthy installation would
pay at every boot for a diagnostic only a moved one needs.

**An RP ID parameter on every `Credentials` method.** Six signatures carrying a
value fixed at construction, and an implementation could still ignore it.

**Deleting abandoned rows on startup or at migration.** A configuration change is
not consent to destroy somebody's registered credentials, and an RPID edited by
mistake would be unrecoverable.

**Listing abandoned keys as present but unusable.** It offers a person a row they
can do nothing with, and the removal rule would need a second notion of what counts.
