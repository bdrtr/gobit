# ADR 0370 — One binding resolves the storefront's customer identity

**Summary:** The five modules that resolve the embedder's customer identity do
it through one binding, and each says only the sentence its operator is warned
with when none is bound.

- **Status:** Accepted
- **Date:** 2026-10-02

## Context

ADR 0057 put the comparison every storefront route makes in one place,
`corehttp.ProvenCustomer`, because a rule copied per module keeps answering
after one copy drifts. The lookup in front of it stayed copied: payment,
customer, order, cart and b2b each carried an `identityBinding` that resolved
`corehttp.IdentityName` on first use, remembered the answer and classified a
failure. The copies agreed line for line except where they meant to differ —
three refused when nothing was bound, two handed the handler nothing — and
D219 is what the copying cost: two of them went on warning of what ADR 0125
had closed.

## Decision

`internal/core/identity.Binding` resolves the customer identity on first use,
once, and classifies what it finds, and every module that names a customer on
its storefront builds one. Whether an absent identity refuses is which door the
module's handler holds — `CustomerID` refuses, `Identity` hands nothing — and
the module supplies only its warning.

## Consequences

- The lazy resolution ADR 0043 argued for, the once-only decision and the
  wiring fault answered as Internal under the module's own setup code are
  written once and tested once.
- The copies' refusal flag had no reader: a module holding `CustomerID` is
  refused whatever a flag says, and one holding `Identity` never set it. The
  door is the choice, so there is no flag.
- A module's warning is its own sentence beside its routes, since only the
  module knows what those routes do without an identity; each module keeps a
  test of that sentence and of its setup code.
- The binding logs "customer identity bound" for all five, with the module
  named, where the copies logged three different messages and two none.
- The test that a binding resolves once counted a constructor's runs, which
  the container's own cache would have satisfied; the shared test registers
  an identity after the first request and expects it unseen.

## Rejected

- Putting it in `core/http`: the binding resolves from the container, which
  `core/http` does not import, and a published helper would be a promise to
  plugins nobody has asked for.
- Keeping the copies with a test that they agree: a test that five files
  agree is a sixth copy watching the other five.
