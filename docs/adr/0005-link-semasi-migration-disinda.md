# ADR 0005 — The link schema is built at declaration time, not in migration files

- **Status:** Accepted
- **Date:** 2026-08-23
- **Phase:** 2

## Context

Plan Section 8 states the migration convention plainly: *"A separate folder per
module; reversible (up/down)."* Link tables do not fit that mould.

The reason is **who** declares the links. According to Plan Section 5.1 modules
declare their link definitions during `Module.Register`, and the plugin system
of Phase 9 requires a plugin to add its own link **without touching the core**.
That is, which link tables will exist is not known at compile time; it cannot be
written as a fixed set of files under `migrations/`.

## Alternatives considered

**A. A single global `links` table in the core** — of the form
`(link_name, from_id, to_id)`. It could be built with a migration. But
cardinality constraints (see the ADR context: `OneToOne` demands uniqueness at
both ends) require a **partial unique index** per link name in a single table;
that again means DDL at runtime for every new link. A single table also becomes
the hot spot of every link.

**B. Generating a migration file per link** — with a code generator. It breaks
the plugin's don't-touch-the-core requirement and adds a build step.

**C. Idempotent DDL at declaration time** — the `Define` call builds the table
and its constraints with `CREATE ... IF NOT EXISTS`.

## Decision

**Alternative C.** `LinkService.Define` builds the schema at declaration time.

Four measures make it safe:

1. **One transaction + an advisory lock.** The declaration runs in a single
   transaction under `pg_advisory_xact_lock`; two processes started at the same
   time do not race each other's DDL.
2. **A durable definition ledger.** The definition is written into the
   `link_definitions` table and compared on every startup. A definition that
   changes silently between versions is caught with `errors.Conflict` — that is
   what takes the place of a migration's version ledger.
3. **Name validation.** A link name must match the pattern
   `^[a-z][a-z0-9_]{0,39}$`, and names colliding with the ledger table or with
   the index namespace are rejected. Because table names cannot be
   parameterised in SQL, validation is the only defence.
4. **Post-DDL verification.** If a relation of **another kind** exists under
   that name, `CREATE ... IF NOT EXISTS` does not error but emits a `NOTICE` and
   skips. So after the DDL it is checked through `pg_class` that the relation
   really is a table and that every required index was created; otherwise the
   transaction is rolled back.

## Consequences

**Positive:** Plugins can add a link without touching the core. The schema sits
in one place together with the definition itself; the two cannot drift apart.

**Negative / known limits**

- **There is no down path.** When a link definition is removed, its table stays
  in the database. This is deliberate: dropping the table automatically would
  mean that a definition temporarily lost to a deployment error deletes every
  bond. Cleanup is an operational decision and is done by hand.
- **`db.Version` does not see the link schema.** The version ledger of module
  migrations is `<owner>_schema_migrations`; the link schema is not written
  there. Whether an environment's link schema is up to date is read from the
  `link_definitions` table.
- **A schema change (e.g. adding a column) needs a migration by hand.** `Define`
  only does "create if absent"; it does not ALTER an existing table. If the
  shape of a link table changes, a core migration must be written.

## Related

- Plan Section 5.2, Section 8 (this ADR is a deliberate exception to that
  convention), Phase 2, Phase 9
- `core/link/service.go` — `declare`, `verifySchema`
