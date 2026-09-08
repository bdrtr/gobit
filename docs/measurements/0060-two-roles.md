# Two roles: what the split closes and what it costs — measured 2026-09-08

Evidence for
[ADR 0060](../adr/0060-the-two-roles-are-the-operators-to-provision.md).

Taken against `postgres:16-alpine` (16.14), the image the shipped compose file
pins. The shipped role was confirmed superuser first — `rolsuper` is `t` for
`gobit` — so everything below is what the split would change, not what is true
today.

The rig is two throwaway roles on a throwaway database, dropped afterwards:

    CREATE ROLE probe_owner LOGIN;      -- owns the schema, runs migrations
    CREATE ROLE probe_app   LOGIN;      -- serves requests
    CREATE DATABASE roleprobe OWNER probe_owner;

As `probe_owner`: an `invoices` table with two rows, a `BEFORE DELETE` trigger
raising a custom SQLSTATE, declared `ENABLE ALWAYS` — the shape migration
`000002_an_issued_invoice_is_retained` actually ships. Then the whole of what
the runtime role is given:

    GRANT USAGE, CREATE ON SCHEMA public TO probe_app;
    GRANT SELECT, INSERT, UPDATE, DELETE ON invoices TO probe_app;

## What the split closes

Every statement below was run as `probe_app`.

| statement | result |
|---|---|
| `DELETE FROM invoices WHERE id='inv-1'` | `ERROR: an issued invoice is retained` |
| `ALTER TABLE invoices DISABLE TRIGGER ...` | `ERROR: must be owner of table invoices` |
| `DROP TRIGGER ... ON invoices` | `ERROR: must be owner of relation invoices` |
| `SET session_replication_role = 'replica'` | `ERROR: permission denied to set parameter` |
| `DROP TABLE invoices` | `ERROR: must be owner of table invoices` |

Rows after all five: 2.

Rows two and three are the two escapes ADR 0032 named as remaining after the
trigger — "it does not stop the role that OWNS the table from dropping or
disabling the trigger". Row five is a third that ADR 0032 did not enumerate, and
the split closes it as well. Row four is not an escape it left open: the
amendment measured it and closed it with `ENABLE ALWAYS`; under a split the
setting is out of reach a second way, because `session_replication_role` is
`SUSET`.

## What the runtime role can still do — the `link.Define` question

The gap ledger's premise was that ADR 0015's privileges row has to move first,
because it requires the application role to run DDL at runtime. Run as
`probe_app`, the statements `LinkService.Define` issues:

| statement | result |
|---|---|
| `CREATE TABLE IF NOT EXISTS link_product_invoice (...)` | `CREATE TABLE` |
| `CREATE UNIQUE INDEX IF NOT EXISTS ... ON link_product_invoice (to_id)` | `CREATE INDEX` |
| `SELECT pg_advisory_xact_lock(...)` | succeeds |

So the privileges row and the split are not in conflict. CREATE on the schema is
not ownership of what a migration wrote, and those are the two different things
the row was read as one of.

## What it costs — the grant surface

A table created by a later migration is not reachable until something grants it:

| step | result |
|---|---|
| `probe_owner` creates `orders`, then `probe_app` reads it | `ERROR: permission denied for table orders` |
| `ALTER DEFAULT PRIVILEGES ... GRANT ... ON TABLES TO probe_app`, then `probe_owner` creates `carts`, then `probe_app` reads it | `0` |
| `probe_app` inserts into an identity column with table INSERT only | `1` — the implicit sequence needs no separate grant |

The grant surface is TABLES and nothing else. Across every migration in the
tree: zero `CREATE SEQUENCE`, zero `CREATE VIEW`, and three `CREATE FUNCTION` —
`invoices_refuse_delete`, `invoice_lines_refuse_delete` and
`invoices_refuse_truncate`, all trigger functions, the last of them behind two
of the migration's four triggers.

The functions needed no grant in the run above — the `DELETE` returned the
trigger's own error rather than a permission denial — but that run does not
PROVE none is needed: PostgreSQL grants `EXECUTE` on a new function to `PUBLIC`
by default, so `probe_app` held it either way. The control that would separate
the two is a `REVOKE EXECUTE ... FROM PUBLIC` before the delete, and it was not
run. What carries the conclusion instead is where PostgreSQL checks the
privilege for a trigger: at `CREATE TRIGGER`, against the role creating the
trigger, not against the role whose statement fires it.

## What it costs — ownership splits both ways

After the runs above, as `probe_owner`:

    relname              | owner
    ---------------------+-------------
    carts                | probe_owner
    invoices             | probe_owner
    link_product_invoice | probe_app
    orders               | probe_owner

    SELECT count(*) FROM link_product_invoice;
    ERROR:  permission denied for table link_product_invoice

The runtime role creates the link tables, so it OWNS them, and the owning role
cannot read them. That includes `link_definitions` — the table ADR 0005 names as
where "whether an environment's link schema is up to date" is read from. In the
shipped tree that is eight link tables plus the ledger: nine tables the runtime
role owns.

## Is the split reachable against today's binary

`serve` always migrates and `Options` has no skip knob, which the ledger read as
a blocker. This was run through gobit's own `db.Migrate` and `db.Version` from a
throwaway command, on a clean database with no default privileges set, one
migration owner named `probe`:

| run | `Migrate` | `Version` |
|---|---|---|
| `probe_owner` applies `000001` | `<nil>` | `1`, not dirty |
| `probe_app`, no grant on the version table | `permission denied for table probe_schema_migrations` (42501), on `SELECT version, dirty FROM ... LIMIT 1` | same error |
| `probe_app`, after `GRANT SELECT ON probe_schema_migrations` | `<nil>` | `1`, not dirty |

So the always-on startup migration is not a blocker: with the schema already
applied, it is a read, and SELECT on the version tables is enough to pass it.
That is what the PROBE ran: a grant per table, which would be one line per
migration owner and 25 of them today. It is not what the list in docs/security.md
ships. That one grants `ON ALL TABLES IN SCHEMA public` and then sets
`ALTER DEFAULT PRIVILEGES`, so it covers the version tables with the rest and
does not grow when a plugin brings a module. The per-table shape is recorded
here because it is what was measured; the shipped shape is the cheaper one and
the two must not be read as the same list.

A fourth run answers the deploy-ordering question. With `000002` present and NOT
yet applied by the owner, `probe_app` starting the server:

    Migrate -> permission denied for table probe_schema_migrations, on
               TRUNCATE "public"."probe_schema_migrations"
    Version -> 1 dirty=false

It fails LOUD and leaves the ledger CLEAN. The server does not open, and the
next `gobit migrate` as the owner is not blocked by a dirty ledger — which is
the property that makes the ordering requirement a nuisance rather than a
hazard.

## What this measurement does not say

- **It was taken on a one-owner toy, not on a full gobit startup.** Nothing here
  ran the 25 migration owners, the modules, the plugins and `link.Define` under
  a split role in one process. The mechanism is measured; the scaling from one
  version table to 25 is asserted, and the grants are per-table rather than
  per-statement, so it is asserted on the shape rather than on a run.
- **It says nothing about a managed provider.** On RDS, Cloud SQL or Neon the
  account handed to the operator may not be able to create a role at all, and
  the owner of the "superuser" account there is already not a superuser.
- **It does not price the provisioning.** The `ALTER DEFAULT PRIVILEGES`
  statement is per grantor role and per schema; a migration run by a different
  role than the one the default privileges name produces tables nothing granted,
  and the failure lands on the first request touching them rather than at
  startup.
