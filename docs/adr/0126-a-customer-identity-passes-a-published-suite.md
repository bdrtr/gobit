# ADR 0126 — A customer identity passes a published suite

**Summary:** `core/identitytest` publishes the rules a `corehttp.Identity` obeys
that hold with no key, no store and no upstream, and a verifier trusting a
stripped header declares it. It costs one published package and buys a mirror
for the code gobit requires and never sees.

- **Status:** Accepted
- **Date:** 2026-09-10

## Context

`corehttp.Identity` is the one interface this framework requires and does not
implement (ADR 0008, ADR 0043). Nothing checked what an embedder wrote against
it, and `docs/known-limits.md` had said so in a sentence for two records: an
implementation that hands the claimed identifier back satisfies the interface,
and the framework cannot tell.

ADR 0125 turned that from a note into a live question. Every storefront route
naming a customer now refuses until a verifier is bound, so binding one is the
only way to serve them — and what gets bound is code that has never been read
by anything in this repository.

The obvious rule does not work. The interface's own contract names "a header
written by an upstream proxy" as a source a verifier may read, and it is right:
behind a gateway that authenticates and strips, that header is proof. What
separates it from the naive implementation is not the header but whether
anything strips it, and no test holding a request can see a gateway.

## Decision

`core/identitytest.Contract` runs the four rules that hold with no key, no store
and no upstream. An implementation whose proof arrives in a header the
deployment strips declares it through `identitytest.UpstreamTrust`, and the
spoofing probe then leaves that header alone.

## Consequences

The declaration is what makes the strongest rule unconditional without
contradicting the interface's contract. The suite's own fixtures are the
argument: two implementations that are the same code, where the one that
declares passes and the one that does not is told, by a failure naming the
interface to implement. It costs a method and buys an author who has looked at
the question.

Four rules is what survived the filter, and the fourth is the one whose symptom
lands furthest from its cause — a verifier that reads the request body leaves the
handler nothing, so every storefront POST fails to parse a body that was there.

The probe's surfaces are a NAMED list, because a request has no schema saying
where a claim may ride. A verifier reading a header not on it is not caught, and
the package says so where the list is written.

Nothing in the tree runs it, and no gate makes anything run it. Every
`corehttp.Identity` here is a test double built to prove whatever its test
needs, so ADR 0077's shape — a gate walking the tree — would be a category
error. The suite is demonstrated by its own fixtures: five implementations that
must fail and one that must pass.

It checks no cryptography and says so in its package doc. A green run means the
shape is not wrong, never that the session scheme is sound.

`core/` gains a package and six published names, kept until 1.0.0 (ADR 0026).

Measurement: [measurements/0126](../measurements/0126-what-a-suite-can-tell-about-a-verifier.md)

## Rejected

**Forbidding a verifier to read a request header.** It refuses a shape the
interface's own contract allows, and the difference is a gateway no test can see.

**A gate that makes the tree's implementations run it.** They are doubles; a
double that passed a compliance suite would have stopped being a double.

**Waiting for a real implementation to exist first.** ADR 0125 made binding one
mandatory for four routes, so the audience is not hypothetical — and the rules
are what the implementation should be written against, not after.

**Adding it to `core/providertest`.** An identity is not a provider, and that
package already exports a function called `Identity` meaning something else.
