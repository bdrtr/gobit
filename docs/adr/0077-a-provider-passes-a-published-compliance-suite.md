# ADR 0077 — A provider passes a PUBLISHED compliance suite, and the in-tree ones run the same suite an embedder gets

**Summary:** `core/providertest` publishes the rules every provider obeys, and a
gate makes the twelve providers in this tree run it.

- **Status:** Accepted
- **Date:** 2026-09-09

## Context

gobit is a library (ADR 0025), so a provider written by somebody else is the
ordinary case. The rules theirs must obey were in `provider.Provider`'s godoc —
the identity "MUST NOT CHANGE from one release to the next", because it is a
registry key, a config value and a durable column — and enforced by nothing.

Twelve providers ship here across six contracts. None of them was checked
against those rules either, which is the sharper half: a repository that cannot
hold its own providers to a sentence has no way to ask anybody else to.

## Decision

**`core/providertest` is published**, taking the surface to eighteen packages
under ADR 0026's promise. `Identity` checks the rules every provider's ID obeys;
`UniqueIdentities` checks a set can share one registry; `Classifier` adds the one
rule the classification contract states that holds with no service behind it.

**It takes an interface, not `*testing.T`.** A published package importing
`testing` puts the testing flags into every binary that links it. `T` is the two
methods the suite needs and `*testing.T` satisfies it with no adapter.

**It asserts by hand — no assertion library.** A published package's
dependencies are the embedder's dependencies, which is why the error reporters
write their own request bodies rather than take an SDK (ADR 0014).

**It checks only what holds WITHOUT the upstream, and says so.** Nothing here
can tell whether a provider talks to its service correctly, and a suite that
pretended to would be worse than none: green would read as "it works".

**The classifier rule asserts the error KIND.** "Fewer than two labels is
refused" is satisfied by a provider that ignored the input, called its service
and got a connection error — exactly the provider the rule exists to catch.
`KindInvalid` says the caller must change the request; a transport failure says
try again, and a scheduled caller branches on the difference.

**Every provider in the tree runs it**, from its OWN package, which is the shape
an embedder's test takes. `TestEveryProviderRunsTheComplianceSuite` walks
`plugins/` and the module tree for a method named `ID` returning a string — the
contract's whole shape, so nothing can leave the population by being what the
population looks for.

## Consequences

- **Twelve providers are now checked against a sentence that was only written.**
  None failed, which is expected and not the point: the rules held because
  everyone used a constant, and luck is not a mechanism.
- **Coverage is per PACKAGE and that is exact today** — twelve packages, one
  provider each. A package growing a second one FAILS, because one call would
  then stand for two providers and the gate would claim more than it checks.
- **An embedder runs the same suite** — the first published package written for
  somebody outside this repository to call in a test.
- **The suite's own rules are proved by a provider that breaks each one.** A
  compliance suite that never fails is decoration, and the failure mode is
  invisible: everybody passes.
- **The exemption map is empty and kept.** The contract has one method, so any
  type with `ID() string` is structurally a provider — including one nobody
  meant. The day one appears the choice is run-the-suite or write-down-why;
  without the map the third option is editing the gate.

## Rejected

- **`internal/providertest`.** A third party could not import it, which is the
  entire purpose.
- **`*testing.T` in the signature.** It drags the testing flags into every
  binary an embedder links.
- **A suite that calls the upstream.** It needs a key, a network and money, so
  nobody runs it — and a suite nobody runs is worse than none.
- **One central test constructing every provider.** It is not what an embedder
  runs, and a suite used differently in-tree drifts from the shipped one.
- **Asserting a lower-case identity.** Registries compare exactly; the rule
  would be invented rather than derived from a failure.
