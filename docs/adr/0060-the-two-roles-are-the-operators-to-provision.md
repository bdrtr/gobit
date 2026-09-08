# ADR 0060 — The two roles are the operator's to provision, and the binary does not change

**Summary:** gobit ships no second DSN and no role management. The migration /
runtime split is an operator's provisioning act, and what it needs from gobit is
a grant list rather than a knob.

- **Status:** Accepted
- **Date:** 2026-09-08

## Context

The migration role and the runtime role are one superuser account, which is why
[ADR 0032](0032-an-issued-invoice-refuses-erasure-in-the-schema.md)'s invoice
refusal is a trigger: a revoke by the account owning the table stops nothing,
and that account can disable the trigger it is refused by. ADR 0032 filed the
split as a gap.

[ADR 0015](0015-postgresql-cluster-contract.md)'s privileges row requires the
application role to run DDL **at runtime**, because a link's schema is declared
in code and written at every startup by `link.Define`
([ADR 0005](0005-link-semasi-migration-disinda.md)). That row stands, and it is
why the split cannot be gobit's default: a runtime role holding CREATE on the
schema creates the link tables and therefore OWNS them, and the migrating role
cannot then read them.

Measurement removed a different belief — that the row has to MOVE first. That
role runs every statement `LinkService.Define` issues while owning none of the
migrated tables, and `serve`'s always-on migration is a read of
`<owner>_schema_migrations` once the owner has applied the schema.

Measurement: [measurements/0060](../measurements/0060-two-roles.md).

## Decision

**The split is the operator's to provision, and gobit's binary does not change.**
One DSN, no role management in a migration, no probe.

gobit cannot create the roles — that needs the privilege the split exists to
remove — so what it owes is the grant list, published where an operator reads
it, in [`security.md`](../security.md): USAGE and CREATE on the schema, DML on
the migrated tables, SELECT on each owner's version table. Nothing beyond
tables; the tree has no sequence and no view, and its three trigger functions
need no grant to the role whose statement fires them.

**ADR 0015's contract table does not grow.** The split SATISFIES the privileges
row rather than changing it, so nothing new is required of the cluster and that
ADR's fifth decision — a needed privilege enters through the table and the probe
together — is not triggered.

## Consequences

- **A table nothing granted is invisible.** A migration adds a table, the
  runtime role gets `permission denied` on first touch, and the `ALTER DEFAULT
  PRIVILEGES` covering it is the operator's, outside anything gobit can test.
- **The schema ends up owned by two roles.** The runtime role creates the eight
  link tables and `link_definitions`, so it owns them, and the migrating role
  cannot read the ledger ADR 0005 names as the answer to whether an
  environment's link schema is current.
- **A deploy that serves before it migrates stops at startup**, on the
  permission error, leaving the ledger clean rather than dirty. Today that
  deploy migrates itself; under a split it has to be ordered.
- **The default installation is unchanged and unprotected.** The compose file
  ships one superuser, so nobody who does nothing gains anything here and
  ADR 0032's trigger stays the whole refusal — which `security.md` now says
  where an operator meets it.

## Rejected

- **Ship a second DSN.** Alone it is worse than nothing: `serve` would migrate
  with the owning DSN, so the process answering requests would hold the owner's
  credentials — the one thing the split exists to prevent. It is useful only
  with a way to skip the startup migration, and that knob is the expensive
  half: it lets a server open against a schema nobody applied.
- **Probe for it at startup.** gobit's shipped configuration is one superuser,
  so the check would fire on every laptop, every `make run` and every CI run — a
  warning against the configuration gobit itself ships is one no reader can act
  on. It would also put a new symbol in a published package (ADR 0026).
- **Move `link.Define` onto the migration role.** It would give the link service
  two pools and split Define from the Create and Delete sharing its object, to
  buy a privilege the measurement says the runtime role can already hold.
