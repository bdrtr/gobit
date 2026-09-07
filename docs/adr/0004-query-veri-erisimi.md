# ADR 0004 — How the Query layer pulls data out of the modules

- **Status:** Accepted
- **Date:** 2026-08-23
- **Phase:** 2

## Context

Plan Section 5.3 describes the Query layer's flow: *pull the records from the
root module → find the related IDs through the links → fetch them in batch from
the related modules' services → merge.*

But the "fetch from the module's service" part runs into a contradiction:

- `core/query` is core, and by **Principle 2.4** it may not know the modules.
- By **ADR 0001** there is no compile-time dependency between modules.
- Query must nevertheless be able to pull data at runtime from modules such as
  `product`, `pricing`, `inventory` that are **not known in advance**.

That is, it is impossible for Query to know at compile time which module it is
asking.

## Alternatives considered

**A. Let Query import the modules** — a direct violation of Principle 2.4; it
would also make every new module a change to the core.

**B. Let Query write SQL directly** — reaching the tables from the core violates
Principle 2.1 (data ownership) and opens the door to cross-module JOINs.

**C. Modules register themselves as a provider** — during `Register` each module
puts a narrow interface into the container under the name `"<module>.query"`.
Query resolves it **by name**. No compile-time dependency, the core knows no
module, and a new module becomes queryable without touching the core.

## Decision

**Alternative C.** `core/query` declares the following narrow interface; modules
register a concrete type that satisfies it:

```go
// Record is a record's field name -> value mapping.
type Record map[string]any

// Provider is the read surface a module opens to the Query layer.
// The module puts it into the container during Register under "<module name>.query".
type Provider interface {
    // Entity is the entity name the provider offers (e.g. "product").
    Entity() string

    // List returns the root records. Query calls it ONLY for the root entity.
    List(ctx context.Context, opts ListOptions) ([]Record, error)

    // FetchByIDs returns the records corresponding to the given IDs.
    // For an ID it cannot find it returns NO record; that is not an error.
    // Query calls it IN BATCH with the ID set coming out of the links (no N+1).
    FetchByIDs(ctx context.Context, ids []string, fields []string) ([]Record, error)
}
```

The provider is a special case of ADR 0001's consumer-side interface pattern:
the **consuming** side (`core/query`) declares the interface, and the providing
module only satisfies the signature and imports nothing.

## Consequences

**Positive**

- A new module does not touch the core to become queryable; all it does is add
  one line of registration inside `Register`.
- Because `FetchByIDs` is a batch call, one call is made per expansion; N+1 is
  structurally prevented.
- Testing Query needs no real module; a fake provider is a few lines.

**Negative / the price**

- Field selection (`fields`) and filtering are left to the provider; Query
  cannot validate them. A provider that sees a field it does not support must
  return `errors.Invalid`.
- `Record` is loosely typed (`map[string]any`). That is the unavoidable price of
  the core not knowing the modules' models; type safety is regained at the API
  boundary (in the store/admin handlers).
- If the provider registration is forgotten, the error surfaces at runtime. In
  that case Query must write **which name was looked up and not found**, with
  `errors.NotFound`.

## Related

- Plan Sections 2.1, 2.4, Section 5.3, Phase 2
- [ADR 0001](0001-modul-arasi-iletisim.md) — the consumer-side interface pattern
- [ADR 0002](0002-di-container-el-yazmasi.md) — resolution by name being
  diagnosable
