# ADR 0128 — Passkeys ship in a module of their own

**Summary:** `contrib/identity-passkey` adds both WebAuthn ceremonies on top of
the session identity, in a separate Go module so the library lands only in the
graph that asked for it. It costs a fourth tree the lanes had to learn and buys a
sign-in with no password to steal.

- **Status:** Accepted
- **Date:** 2026-09-11

## Context

ADR 0127 shipped a working customer identity and deferred passkeys to a record of
its own, because the library was the one part with a price. The price was then
measured: importing go-webauthn adds nine modules gobit's graph does not carry,
`go-tpm` and `go-tpm-tools` among them — attestation support for hardware most
shops will never see.

`contrib/identity-session` is imported by installations that wanted a password.
Putting passkeys in it would put an attestation-format parser in every one of
their module graphs, vulnerability scans and legal reviews, which is the sentence
the dependency gate already writes about gobit itself.

## Decision

Passkeys are a separate Go module that imports the session one. It performs both
ceremonies, owns a table of credentials, and signs a person in by issuing the
SAME session cookie a password issues.

## Consequences

The ceremony state between begin and finish is a short-lived cookie sealed with
the session module's key, so this module holds no secret of its own. That needed
one thing from the other: its MAC now carries what a signed value is FOR, because
one key signing two shapes makes them interchangeable and a ceremony payload is
something a caller can often steer. The separator between purpose and payload is
asserted directly, since today's two purposes are the same length and no
behaviour would notice it going.

A sign-in names nobody. The authenticator asks the person which of their keys is
for this site, which is the better flow and also the only one that is not an
account enumerator — a begin that took an e-mail would answer differently for an
address with keys and one without.

A registration adds a key to the account the request PROVES, and the ceremony is
bound to the account that began it: signing in as somebody else between the two
calls does not move the key. The finish clears the ceremony cookie, because a
challenge is single-use and the library cannot notice a second use — the
signature over it is genuinely valid.

The credentials are stored as the library serialises them, in one JSONB column.
A column per field would be a migration for every field the library grows, and
what this module needs from a credential is the id and the owner, which are
columns. The promise that shape makes is narrow and tested as such: a key written
to Postgres and read back still signs its owner in.

It registers no listing and no removal: a delete endpoint that can remove
somebody's LAST key turns a convenience into a lockout. The module exports its
credential store, so an embedder who has decided is not blocked.

`examples/starter` does not add it — it needs a real domain, and a starter
shipping a wrong relying party id would mint credentials the site that created
them cannot use, silently.

Measurement: [measurements/0128](../measurements/0128-what-a-passkey-costs-a-graph.md)

## Rejected

**Putting it in `contrib/identity-session`.** Nine modules, including TPM
attestation, in the graph of every installation that wanted a password.

**A non-discoverable sign-in.** It needs the caller to say who they are first,
which brings back the account enumerator the password sign-in refuses to be.

**Storing the ceremony in a table.** A write, a read and a sweep for something
that expires in two minutes and belongs to one browser.

**Testing the handlers without a real authenticator.** Every mistake this module
can make is about a challenge, an origin or a user handle, and no assertion about
a handler sees any of them.
