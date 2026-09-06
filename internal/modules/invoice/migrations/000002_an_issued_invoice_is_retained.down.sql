-- Rolling 000002 back: the four triggers, the three functions and the index go.
--
-- The order is not decoration. A function that a trigger still uses cannot be
-- dropped — PostgreSQL refuses it as a dependency — so the triggers go first
-- and the functions after. Writing DROP FUNCTION ... CASCADE instead would work
-- and is exactly what must not be written here: CASCADE drops whatever happens
-- to depend on the function, which on a database where somebody has added a
-- trigger of their own means dropping that too, silently, in a rollback that
-- was asked to undo one migration.
--
-- EVERY object created by the up file is named below, and that completeness is
-- what internal/arch TestMigrationsCanReallyBeRolledBack actually measures. It
-- runs up, down, and up AGAIN. A leftover trigger or function does not fail the
-- down — nothing checks the catalog — it fails the SECOND up, on a
-- "trigger already exists" or "function already exists" that names an object
-- the reader has to go and find. The IF EXISTS clauses make this file safe to
-- run against a database that never got the up, which is the state a failed
-- migration leaves behind.
--
-- Rolling this back RESTORES THE HAZARD ADR 0032 closed: with these objects
-- gone, DELETE FROM invoices succeeds again and the numbered series can acquire
-- a hole. That is what rolling back this migration means, and it is written
-- here so nobody discovers it afterwards.

DROP TRIGGER IF EXISTS invoices_no_delete ON invoices;
DROP TRIGGER IF EXISTS invoice_lines_no_delete ON invoice_lines;
DROP TRIGGER IF EXISTS invoices_no_truncate ON invoices;
DROP TRIGGER IF EXISTS invoice_lines_no_truncate ON invoice_lines;

DROP FUNCTION IF EXISTS invoices_refuse_delete();
DROP FUNCTION IF EXISTS invoice_lines_refuse_delete();
DROP FUNCTION IF EXISTS invoices_refuse_truncate();

DROP INDEX IF EXISTS invoices_buyer_email_idx;
