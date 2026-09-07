# ADR 0001 — Cross-module communication: a consumer-side interface

- **Status:** Accepted
- **Date:** 2026-08-23
- **Phase:** 0 (implementation begins in Phase 4)

## Context

The two invariant rules in Section 2 of the implementation plan contradict each
other when read literally:

> **2.1 Module isolation:** … Access is only through the other module's **public
> service interface**.

> **2.4 Dependency direction:** … Modules do **not depend on each other at
> compile time** (only interface packages are shared).

In Go, interfaces live in packages. If the `cart` module imports
`internal/modules/product/service` in order to use the `product` module's
interface, that is a **compile-time dependency** — 2.4 is violated. So 2.1 and
2.4 cannot both hold under the reading "the owner publishes the interface".

This ambiguity surfaces for the first time in Phase 4
(`product ↔ pricing ↔ inventory`), and destructively in Phase 6 (the
`complete_cart` saga touches five modules). A wrong decision taken in Phase 4
leaves behind a dependency web that is expensive to untangle across every module
that follows.

## Alternatives considered

**A. The owner publishes the interface** — `cart` imports the `product/service`
package. Satisfies 2.1, violates 2.4. The modules are welded together at compile
time; lifting one module out into a separate service breaks the import graph.

**B. A shared contract package** — a neutral package such as
`internal/contracts/product`. Both modules depend on it, neither on the other.
It works; but it brings a second package per module, contract and implementation
drifting apart in separate places, and the question "whose contract package is
it?". A contract package also tends over time to become a god-package that every
module knows.

**C. A consumer-side interface** — the module that has a need defines the
**narrow** interface it needs **in its own package**. The provider's concrete
type satisfies that interface structurally (structural typing); no import is
required.

## Decision

**Option C.** Every cross-module dependency is established through a narrow
interface the consumer defines in its own package. The concrete implementation
is resolved from the container by name at runtime.

This choice satisfies 2.4 literally (zero compile-time dependencies), preserves
the intent of 2.1 (access only through the service contract), and coincides with
Go's own advice: *"the consuming side defines the interface; the producing side
returns a concrete type."*

### The pattern

```go
// internal/modules/cart/service/service.go
package service

// ProductReader is the ONE capability cart needs from the product module.
// It is deliberately narrow: it declares not the whole of product's service but
// only what is used here. The product package is NOT imported.
type ProductReader interface {
    GetVariant(ctx context.Context, variantID string) (Variant, error)
}

// Variant holds the fields cart needs; it is not product's model.
type Variant struct {
    ID    string
    Title string
}

type Service struct {
    products ProductReader // resolved from the container under the name "product.service"
}
```

The provider side does nothing: as long as `product`'s concrete service carries
the `GetVariant` method, it satisfies this interface.

### Enforcement

The `depguard` rules in `.golangci.yml` forbid **every** cross-module import
(~~12 modules × 11 forbidden packages~~ **17 × 16 as of 2026-09-07**). A
violation of this ADR is caught in CI before compilation — the rule is a gate,
not a comment.

**The paragraph below turned out to be the exact instruction nobody followed,
and it is worth reading as evidence rather than as advice.** On 2026-09-07 the
matrix was measured against the module tree: it named fifteen modules, and
`invoice` and `review` appeared nowhere in the file — neither with a section of
their own, nor as a deny target in anybody else's. The rule was still enforced,
because `TestModulesDoNotImportEachOther` walks the real directories, so nothing
went wrong; the lint half simply covered less of the tree with every module
added. The matrix is complete again, and
`TestTheDepguardMatrixNamesEveryModule` now fails the day it is not — a written
instruction is not enforcement.

When a new module is added, both the new module's own rule must be added to the
`depguard.rules` list and the new module must be added to the deny list of every
existing rule.

## Consequences

**Positive**

- Zero compile-time dependencies between modules; any module can be lifted out
  into a separate service without breaking the import graph.
- A consumer binds only to the surface it actually uses; as the provider's
  interface grows, consumers are unaffected.
- Testing is easy: a fake implementation of a narrow interface is a few lines.

**Negative / the price**

- The same concept (say, a variant's id and title) is redeclared as small DTOs
  in several modules. This is the **accepted price** of isolation; the urge to
  set up a shared model package must be resisted.
- When a provider changes a method signature the compiler does not warn the
  consumer; the mismatch surfaces at the moment of resolution from the
  container. To compensate: a compile-time check is performed at registration
  into the container, and an integration test is mandatory for every consumer.

### Container registration check

The provider module verifies that it satisfies the surface its consumers expect
not with a `var _` declaration in its own package (that would require an
import), but **in an integration test**. Because Phase 1's
`container.Resolve[T]` resolves with a type parameter, a mismatch comes back as
an explicit, typed error:

```go
products, err := container.Resolve[cartservice.ProductReader](c, "product.service")
// err: "product.service does not satisfy the cartservice.ProductReader interface"
```

## Related

- Plan Sections 2.1, 2.4 — clarified by this ADR
- Plan Section 5.1 — `Container.Provide(name, ctor)` / `Resolve[T](c, name)`
- Plan Phase 4 — the first real application (`product ↔ pricing ↔ inventory`)
