# ADR 0002 — DI container: hand-written instead of a library

**Summary:** `core/container` is written by hand instead of taking the
`samber/do` dependency. The contract is small enough that the library would
cost more in surface than it saves in code.

- **Status:** Accepted
- **Date:** 2026-08-23
- **Phase:** 1

## Context

Plan Section 3 recommends `samber/do` v2 for DI. The contract in Section 5.1,
however, is binding:

```go
func (c *Container) Provide(name string, ctor any) error
func Resolve[T any](c *Container, name string) (T, error)
```

That is, **named registration** + **a constructor taken as `any`** +
**generic resolution**.

`samber/do` v2's registration surface, by contrast, is type-parameterized
(`do.ProvideNamed[T]`). The only way to build a `Provide` that takes `any` on
top of it is to hand every service to do as `any` — and at that moment all three
of the things do brings are lost.

## Evaluation

| What do offers | What happens once it is flattened to `any` |
|---|---|
| Typed error messages | Because the type information is gone, the "registered concrete type vs expected type" diagnosis ADR 0001 asks for cannot be produced |
| Double-registration protection | do **panics**; the contract asks for `errors.Conflict` |
| Shutdown | do shuts down according to its own dependency graph and recognizes only its own `Shutdowner` interface; the contract requires the **reverse of registration order** and support for `io.Closer` |

What was left of do was a map with a mutex.

## Decision

`core/container` **writes the behavior the contract asks for directly**; the
`samber/do` dependency is not added.

Because only the surface from Section 5.1 is visible from the outside, the
decision is reversible: the body can later be moved onto a library without
affecting callers.

The behaviors the package provides beyond the contract:

- **Lazy singleton** — the constructor runs exactly once, on the first
  `Resolve`, even under 100 concurrent calls.
- **Dependency cycle detection** — `A -> B -> A` returns a clear error carrying
  the wait graph instead of deadlocking.
- **Diagnosable type mismatch** — the error message names both the registered
  concrete type and the expected interface, and the missing or mismatched
  method. Because in ADR 0001's consumer-side interface pattern a mismatch is
  caught at runtime rather than by the compiler, message quality is critical.
- **Shutdown in reverse order** — services implementing `io.Closer` and
  `Shutdowner` are closed in the reverse of registration order; panics are
  caught, errors are joined.

## Consequences

**Positive:** The contract is met exactly, the dependency count does not grow,
and error messages can be shaped to the needs of the field.

**Negative:** Concurrency and shutdown ordering are now our responsibility. In
return, the package is heavily tested (concurrent constructor, cycle,
resolution racing a shutdown, a service that panics).

**Known limit:** `Shutdown` waits for an in-flight constructor only as long as
the ctx budget it was given. If the budget runs out that service is left
unclosed, and this is written into the joined error `Shutdown` returns. The
practical consequence: the time given to `Shutdown` must be longer than the
slowest constructor.

## Related

- Plan Section 3 (updated by this ADR), Section 5.1
- [ADR 0001](0001-modul-arasi-iletisim.md) — the origin of the need for a diagnosable type mismatch
