# ADR 0127 — A working identity ships outside the module

**Summary:** `contrib/identity-session` is a working customer identity with its
own `go.mod`: a signed cookie, argon2id passwords, its own table and two
storefront endpoints. It costs a tree four gates had to learn about and buys an
installation that can actually sign somebody in.

- **Status:** Accepted
- **Date:** 2026-09-11

## Context

gobit requires a `corehttp.Identity` and issues none (ADR 0008, ADR 0043). Since
ADR 0125 every storefront route naming a customer refuses until one is bound, so
binding one stopped being advice. What the tree offered an embedder to bind was
nothing: every implementation here is a test double, and ADR 0126 published the
rules for one without publishing one.

An example was considered and refused before this record: a real shop copies an
example and inherits its security debt as its own, and a session that is a
demonstration is a session nobody maintains.

## Decision

A working identity ships in this repository as `contrib/identity-session`, a
SEPARATE Go module an embedder imports and adds. The first slice is a signed
cookie and argon2id passwords; passkeys are a record of their own.

## Consequences

The separate `go.mod` is the whole placement argument. `plugins/*` are in the
main module, so a plugin's requires are every embedder's requires — their module
graph, their vulnerability scan, their legal review, in the dependency gate's own
words — and this package grows towards WebAuthn. A library for that belongs in
the graph of whoever asked for it. The first slice adds nothing to anybody:
argon2id needs `golang.org/x/crypto`, which gobit already requires directly.

gobit's own arch gates do not walk the tree, and what holds the package instead
is the suite gobit published for exactly this interface — which found a real
defect in it, not only in its own fixtures.

Four gates and two lanes learned about the tree and each found something. The
language detector refused it before reading a byte, because a new root is how a
population grows past a gate. The response-writing gate rejected a hand-written
error envelope in five places, so the module answers through `corehttp.WriteError`
like the routes beside it. Renaming the out-of-tree compilation gate to stop
saying "examples" was reverted: two records below 0052 name that test, and this
repository does not rewrite those. And twenty-eight tests nothing ran now have a
lane with a floor, because "no failures" and "I ran nothing" must not share an
exit code.

A signed cookie cannot be revoked before it expires. That is the price of keeping
the identity off the read path of twelve storefront routes, and it is written
where an operator reads it rather than discovered.

The module registers no storefront sign-up. Credentials are written by an
operator endpoint, because self-registration needs e-mail verification, a rate
limit and a decision about who may create a customer, and none of those is a
session's business.

Measurement: [measurements/0127](../measurements/0127-where-a-session-package-belongs.md)

## Rejected

**An example under `examples/`.** A real shop copies an example and owns the
copy. An example is not wrong for a real shop; it is simply not maintained for
one, and a session is the last thing to learn that from.

**A plugin under `plugins/`.** Installable with one environment variable, which
is genuinely better, and it puts a WebAuthn library in the graph of every shop
that wanted a product catalogue.

**A published package under `core/`.** Every cost of the plugin plus a signature
frozen until 1.0.0 (ADR 0026), on the one concern ADR 0008 decided the core
would not own.

**Shipping passkeys in the same slice.** The library choice is a measurement of
its own, and the cookie-and-password half is useful with no dependency at all.
