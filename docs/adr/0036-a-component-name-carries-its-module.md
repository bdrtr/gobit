# ADR 0036 — A schema component name carries the module that owns it

- **Status:** Accepted
- **Date:** 2026-09-07
- **Phase:** after the roadmap

## Context

An OpenAPI component's name was derived from the Go type name alone: the first
letter upper-cased, a trailing `DTO` dropped. Both normalizations are lossless
and both exist for the same reason — being unexported and being a transfer
object are Go concepts, not HTTP contract ones.

What that rule could not express is OWNERSHIP. Two modules cannot both have a
type called `Address`, and the failure is not proportional: when two different
types want one component name, `Doc.Build` returns an error and `/openapi.json`
returns 500 for **every** module in the installation. One module's naming choice
takes down another module's schema.

**Four modules hit it, independently, and all four made the same call.** Rather
than take the document down they left the affected endpoints UNDESCRIBED — the
endpoints still appear with their path, method and security, only without a
body. Twenty-five endpoints in total:

| module | type | collides with | wanted |
|---|---|---|---|
| `customer/api` | `addressDTO`, `addressRequest` | `cart/api` | `Address` |
| `payment/api` | `collectionDTO` | `product/api` | `Collection` |
| `fulfillment/api` | `optionDTO` | `product/models` | `Option` |
| `order/api` | `lineItemDTO` (via `orderDetailDTO`) | `cart/api` | `LineItem` |

All four also wrote down, in their own words, that the fix belonged in the core
and not in either module: a component name is the CLASS NAME a client generator
produces, so it is published contract, and a decision that concerns two modules
at once cannot be taken from inside one of them without breaking the other's
client unannounced. They were right on every count.

The reason this sat still is that it cost nothing visible. A bodiless endpoint
does not look missing — it looks like an endpoint that takes nothing — and until
[ADR 0035](0035-the-schema-vocabulary-is-published.md) published the schema
package and added `Doc.UndescribedRoutes`, nothing in the repository counted
them.

## Decision

**A component's name is prefixed with the name of the module being described.**

`customer/api.addressDTO` becomes `CustomerAddress` and `cart/api.addressDTO`
becomes `CartAddress`. The prefix is DROPPED when the type name already begins
with it, so the product module's `Product` stays `Product` rather than becoming
`ProductProduct`; the comparison is case-insensitive on the first letter only,
which is exactly the difference the existing upper-casing creates.

**The namespace is the module's own `Name()`, and the composition root applies
it.** `Doc.ForModule(name, describe)` sets it around each module's `Describe`
call, in the one place that holds both the module list and the document — the
same place, and for the same reason, that resolves the `Describer` type
assertion.

**A registration outside any module keeps the bare name.** Those components
belong to gobit rather than to a module, and there is no module name to give
them.

## Rejected alternatives

**Derive the namespace from the type's import path.** The obvious alternative,
and it needs no call at the root. Rejected because it is a GUESS that cannot
fail loudly: it has to know that `api`, `models` and `service` are role segments
to step over, it has no answer for an embedder whose types live in a layout it
has never seen, and a wrong guess does not produce an error — it produces a
wrong class name in every generated client. `module.Module` already carries a
Name; it is already the module's identity for its container services and its
migration table, and an out-of-tree module has one for free.

**Prefix only when there is a collision.** It renames the fewest names, and it is
the only candidate that is ORDER-DEPENDENT: which of two colliding types keeps
the bare name would depend on registration order. A published contract that
depends on the order modules were added is worse than one that renames more.

There is a second, quieter version of the same fault: making the prefix depend on
which modules are INSTALLED. A shop running only the customer module would get
`Address`; one that also runs cart would get `CustomerAddress`. The contract
would then differ between two installations of the same version.

**Let each module declare its own component names.** Nothing renames unless
somebody asks, and the clash detector still refuses a silent collision. Rejected
because it puts a published name behind a per-type decision — the thing that
drifts — and because it leaves the default rule exactly as broken as it was for
anybody who does not know to use it.

**Rename the colliding types in the modules.** What all four modules refused, and
they were right: it is four separate decisions where one is needed, each one
breaking another module's client, and the next collision starts the argument
again.

## Consequences

**Positive**

- **The twenty-five endpoints have bodies**, and the ledger the ADR 0035 audit
  reads fell from thirty-eight to two in the same day.
- **A new module cannot take a name another module needs.** The failure mode
  that made four modules retreat is gone by construction rather than by care.
- **The name says where the type comes from.** `CartLineItem` and `OrderLineItem`
  are two classes in a generated client that a reader can tell apart, which the
  single `LineItem` never was.

**Negative, and accepted**

- **It is a RENAME in the published contract**, and component names are what
  client generators turn into class names. `core/openapi` was published one
  decision earlier (ADR 0035), so this is a breaking change to a published
  package on its second day. It is accepted under the repository's stated `0.x`
  policy — breaking changes may arrive in minor versions until `1.0.0` — and it
  is better paid now than after a client exists.
- **The prefix is applied by a caller, so a caller can forget it.** The e2e
  harness DID: it keeps its own copy of the describe loop, the namespace landed
  in production first, and the harness's document then failed to build at all
  because customer and cart collided in the copy nobody had changed. The pair is
  now audited from outside by `TestTheDescribeLoopsAgree`, on the same principle
  as the module-set audit next to it: what enforces a promise is not a line
  written beside it.
- **The "already prefixed" clause can itself collide.** A module owning both
  `paymentCollectionDTO` and `collectionDTO` would have both resolve to
  `PaymentCollection`. That is not silent — it is exactly the clash the existing
  detector reports — but it is a shape somebody will meet.

## What this deliberately does NOT do

- **It does not make `Describer` mandatory.** ADR 0035's reasoning stands: an
  undescribed module is a valid model. This decision removes an obstacle to
  describing, not the choice.
- **It does not namespace the core's shared components.** `Error` and `List`
  stay reserved and bare; they belong to gobit, they are referenced by every
  endpoint in the document, and a module that tried to take one of those names
  is already refused.
- **It does not fix the last two undescribed endpoints.** The search plugin's
  two are a translation question rather than a naming one — its package is still
  Turkish, and ADR 0012's ratchet says the file that would describe them has to
  be English.

## Related

- [ADR 0035](0035-the-schema-vocabulary-is-published.md) — published the package
  and built the audit that counted the twenty-five.
- [ADR 0026](0026-the-published-surface-is-fourteen-packages.md) — what
  publishing a package promises, which is what makes this a breaking change.
- [ADR 0012](0012-repository-language-and-solid.md) — the language ratchet the
  last two endpoints are waiting on.
