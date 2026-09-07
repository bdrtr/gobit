-- An issued invoice refuses erasure, and the refusal lives in the SCHEMA.
--
-- ADR 0032 decides that the erasure contract (core/erasure, ADR 0029) answers
-- RETAINED for an invoice and that the refusal is enforced by the database
-- rather than by the module, "because a refusal that lives in Go stops the
-- caller while a refusal that lives in the database stops the STATEMENT". That
-- ADR deliberately left the MECHANISM open and asked whoever wrote this file to
-- say which one and why. This is that answer.
--
-- # Why a trigger and not REVOKE DELETE, measured rather than argued
--
-- The revoke candidate does not work in this repository, and the reason is the
-- deployment rather than the grammar. gobit ships ONE database role and it is a
-- SUPERUSER: deploy/docker-compose.yml sets POSTGRES_USER to gobit, and
-- config.Config carries exactly one DATABASE_URL, so the role that runs the
-- migrations and the role that serves requests are the same superuser. Measured
-- against the running database:
--
--   REVOKE DELETE ON invoices FROM gobit;   -- reports REVOKE
--   DELETE FROM invoices WHERE id = ...;    -- reports DELETE 1
--
-- The revoke SUCCEEDS. It visibly changes pg_class.relacl, so an operator
-- reading the catalog sees an access control list that looks like protection,
-- and the very next DELETE still removes the row, because a superuser bypasses
-- every permission check. A guard that reports success and stops nothing is
-- worse than no guard at all: it leaves a catalog artefact that the next reader
-- will trust. Making it real would mean introducing a second, non-superuser
-- runtime role, which is a change to the deployment contract and to
-- config.Config, and ADR 0032 asks for a refusal, not for role management.
--
-- Re-measured on a fresh database with 000001 and this file applied, with the
-- two delete triggers dropped first so that the permission check is the only
-- thing left to stop the statement: the revoke reported REVOKE, relacl became
-- {gobit=arwDxt/gobit} with the "d" privilege gone, and the DELETE reported
-- DELETE 1.
--
-- The trigger is the better of the two candidates, and it is better by a
-- MARGIN rather than absolutely. It stops the ordinary statement: an
-- unqualified DELETE or TRUNCATE against either table, from the superuser role
-- gobit ships and from a plain role alike, comes back GB001 with the row still
-- there. REVOKE stops nothing at all. What the trigger does NOT do is survive
-- an operator who means it, and the previous version of this header said
-- otherwise.
--
-- # What the trigger does NOT stop, measured rather than assumed
--
-- This header used to end the section above with "a trigger is not bypassed by
-- superuser; it is the only one of the two candidates that is TRUE of the
-- database gobit actually ships". That sentence is FALSE and was measured
-- false. As the gobit superuser, against these very migrations:
--
--   BEGIN;
--   SET LOCAL session_replication_role = replica;
--   DELETE FROM invoices WHERE id = 'i1';   -- DELETE 1, the row was gone
--
-- User triggers do not fire while session_replication_role is replica — that
-- is what the setting exists for — so all four triggers below go quiet at once
-- and not just the one being escaped: TRUNCATE invoices, invoice_lines in the
-- same session was measured succeeding the same way.
--
-- So the guard has TWO escapes and only the first is sanctioned:
--
--   1. ALTER TABLE ... DISABLE TRIGGER. Two statements to lift and two to put
--      back (spelled out further down). It is deliberate, it stays visible in
--      pg_trigger.tgenabled while it is in force, and it leaves the foreign
--      key alone, so a document that is removed comes apart whole.
--   2. SET session_replication_role = replica. ONE statement, session-local,
--      leaving no trace in the catalog and gone when the session ends. Nobody
--      sanctioned it, and it is worse in a second way that was also measured:
--      the ON DELETE CASCADE on invoice_lines.invoice_id is itself an internal
--      trigger, so it is silenced too. Committed on a scratch database, the
--      DELETE above left invoices holding 0 rows and invoice_lines holding 1 —
--      an ORPHAN line the foreign key would never have permitted. The escape
--      that skips the friction skips the referential integrity with it.
--
-- # The limit is the ROLE, and this repository does not close it
--
-- Both escapes are open to gobit for one reason: gobit ships a single role that
-- both OWNS these tables and serves requests. Measured against a second role
-- created NOSUPERUSER and granted nothing but DML on these tables:
--
--   SET session_replication_role = replica;    -- ERROR: permission denied to
--                                              -- set parameter (its
--                                              -- pg_settings.context is
--                                              -- "superuser")
--   ALTER TABLE invoices DISABLE TRIGGER ...;  -- ERROR: must be owner of table
--   DELETE FROM invoices WHERE id = 'i1';      -- GB001, the refusal stands
--
-- A guard that the application's own role can lift is a role-separation
-- problem, not a trigger problem, and this repository does not solve it today:
-- config.Config carries exactly one DATABASE_URL, so there is no second role to
-- run as, and introducing one is a change to the deployment contract that
-- ADR 0032 did not ask for. The honest statement of what this migration buys is
-- therefore: it stops the statement nobody meant to run, and it does not stop
-- an operator who means it. That is strictly more than REVOKE buys, which is
-- why the decision stands — but it is a smaller claim than the one this file
-- used to make, and the next person is building on this paragraph rather than
-- on that one.
--
-- That mitigation IS taken here: ALTER TABLE ... ENABLE ALWAYS TRIGGER makes a
-- trigger fire in replica mode too, and with it in force the DELETE above came
-- back GB001. It is applied to all four triggers below.
--
-- An earlier draft of this header argued for leaving it out, on the ground that
-- the migration had already been applied to live databases and a change now
-- would protect only databases created later. That premise was FALSE and was
-- caught by checking it: this file has never been in a commit, so there is no
-- installation carrying the weaker version and nothing to be consistent with.
-- The lesson is worth more than the paragraph it replaces — an argument for not
-- doing something is still an argument, and it has to be true.
--
-- What ENABLE ALWAYS does NOT buy is a guard against the role that owns the
-- table. Two escapes remain and both are deliberate DDL: ALTER TABLE ...
-- DISABLE TRIGGER, and dropping the trigger outright. That is the shape this
-- refusal was always meant to have, and it is now the shape it actually has.
--
-- # The measured hole: guarding invoices alone leaves the document gutted
--
-- With the invoices trigger in place and nothing on the child table,
--
--   DELETE FROM invoice_lines WHERE invoice_id = ...;
--
-- still reported DELETE 1. That takes the retained description text off the
-- document and leaves the invoice standing with its STORED totals — which 000001
-- stores precisely so that the printed document cannot be recomputed away —
-- against lines that no longer exist. A guard on the parent alone protects the
-- number and loses the document. Both tables are guarded below.
--
-- # A ROW trigger, not a statement trigger
--
-- BEFORE DELETE FOR EACH ROW fires once per row that is actually being removed,
-- so "DELETE FROM invoices WHERE id = <nonexistent>" stays a harmless DELETE 0.
-- A statement trigger fires once per statement whether or not it matched
-- anything, and would turn "there was nothing to delete" into a refusal — an
-- answer that is not true and that a cleanup script cannot tell apart from a
-- real one. The row trigger also has OLD, which is what lets the message name
-- the invoice id and its NUMBER; naming what was kept is exactly the debt the
-- word RETAINED carries in core/erasure.
--
-- TRUNCATE does not fire a row trigger at all — it removes rows without
-- visiting them — so each table gets a BEFORE TRUNCATE FOR EACH STATEMENT
-- companion. A statement trigger has no OLD, so it cannot share a function with
-- the row triggers and gets its own; it names the table through TG_TABLE_NAME.
--
-- Measured while these were being written, and worth knowing before someone
-- decides the truncate companion is redundant: "TRUNCATE invoices" ALONE never
-- reaches the trigger, because PostgreSQL refuses it first with 0A000, "cannot
-- truncate a table referenced in a foreign key constraint". The forms that DO
-- reach it are "TRUNCATE invoices, invoice_lines" and "TRUNCATE invoices
-- CASCADE", and both were measured returning GB001. So the FK on the child was
-- already stopping one of the three spellings by accident; the companion is
-- what stops the other two, and it is the only thing standing between the
-- series and an operator who reached for CASCADE.
--
-- # The bodies are SINGLE-QUOTED string literals, never dollar-quoted
--
-- This is a constraint of this repository rather than a style preference. The
-- SQL audits in internal/arch read every .sql file by blanking comments and
-- QUOTED STRING LITERALS to spaces and then splitting the remainder into
-- statements on ";" (see blankSQLNoise and the schema replay in
-- internal/arch/columns_test.go). They do not understand dollar quoting. A
-- dollar-quoted body would therefore be read as live SQL, its internal
-- semicolons would cut the file into fragments no scanner can classify, and the
-- audits would start reporting nonsense about a module that had done nothing
-- wrong. A single-quoted body is blanked away exactly like any other literal.
--
-- Two rules follow from that and they are load-bearing: nothing inside a body
-- may contain a "--" sequence (the comment blanking runs BEFORE the quote
-- blanking, so a "--" inside a literal would silently kill the rest of that
-- line for the scanner), and every quote inside a body is doubled.
--
-- # A CUSTOM SQLSTATE, not restrict_violation
--
-- The refusal raises SQLSTATE GB001. The obvious alternative, 23001
-- (restrict_violation), was rejected because it is ALSO what a genuine
-- FOREIGN KEY ... ON DELETE RESTRICT raises: an embedder mapping 23001 onto
-- "this is the invoice retention refusal" would misclassify every real
-- constraint failure it ever met, and the misclassification would be invisible
-- because both are refusals to delete. PostgreSQL assigns no class beginning
-- with the letters G and B (its own classes run 00 to 58, plus F0, HV, P0 and
-- XX), so GB001 cannot collide with a condition the server raises by itself.
-- The code is declared once on the Go side, beside the module's other SQLSTATE
-- constants in repository/convert.go, and mapped to the module's retention
-- error there.
--
-- # This is the FIRST procedural code in any migration in this repository
--
-- Verified before it was written: a case-insensitive search across all 157 .sql
-- files in the tree for CREATE TRIGGER, CREATE FUNCTION, CREATE RULE, CREATE
-- EXTENSION, the word plpgsql and the dollar-quote sequence returned NOTHING.
-- ADR 0015 records the cluster contract as "extensions: none" and evidences it
-- with a grep for CREATE EXTENSION; that evidence still holds and this file
-- adds no such statement, because plpgsql is installed by initdb into every
-- database from template1 on every supported image. Nothing here has to be
-- enabled, requested or checked for at deploy time.
--
-- # The CASCADE stays, and the SANCTIONED escape is TWO statements
--
-- invoice_lines.invoice_id keeps its ON DELETE CASCADE. Removing it would
-- change what a sanctioned deletion MEANS — an operator who has decided to
-- remove a document would then have to remember to remove its lines, and a
-- half-removed document is a worse state than either end. What changes is the
-- shape of the escape. The child trigger fires on the rows the CASCADE deletes,
-- so disabling the parent trigger alone is no longer enough: the sanctioned
-- escape is TWO statements, and two more to put it back, inside one
-- transaction.
--
--   ALTER TABLE invoices      DISABLE TRIGGER invoices_no_delete;
--   ALTER TABLE invoice_lines DISABLE TRIGGER invoice_lines_no_delete;
--   ... the deliberate DELETE ...
--   ALTER TABLE invoice_lines ENABLE TRIGGER invoice_lines_no_delete;
--   ALTER TABLE invoices      ENABLE TRIGGER invoices_no_delete;
--
-- That friction is the point, and ADR 0032 accepts it by name: "the escape is a
-- DBA acting deliberately, which is the right shape for this class of act".
--
-- "Two statements" describes the SANCTIONED route and nothing more. ~~It is not
-- a lower bound on what it costs to delete a document: the unsanctioned route
-- measured above is ONE session-level SET that silences all four triggers and
-- the CASCADE with them, and this file cannot stop it while the application
-- runs as the owner of these tables.~~ **Corrected 2026-09-07: the one-SET
-- route is CLOSED**, by the four ALTER TABLE ... ENABLE ALWAYS TRIGGER
-- statements at the foot of this file. In replica mode the DELETE comes back
-- GB001 and the CASCADE is never reached, so the paragraph struck above records
-- the hole as it was MEASURED and not as this migration ships. What is left to
-- the owning role is deliberate DDL and nothing cheaper: DISABLE TRIGGER, or
-- dropping the trigger outright. The warning survives the correction, because
-- it was never really about the SET. A reader who takes the four ALTERs above
-- as the cost of a deletion is still reading a description of good practice as
-- if it were a guarantee: the role that lifts the guard is the same role that
-- decides whether to put it back, and nothing in the schema makes it. That is
-- the mistake the section "What the trigger does NOT stop" exists to prevent.
--
-- # Why an index arrives in a refusal migration
--
-- The erasure contract has to resolve a person before it can report how many
-- rows are retained, and this table has NO customer_id and NO order_id column:
-- the only handle on a person is the address printed on the document. Measured
-- on 000001, the table carries three indexes — invoices_number_uniq,
-- invoices_listing_idx and invoices_series_idx — and NONE of them can serve a
-- lookup by buyer e-mail, so the count behind every RETAINED answer would be a
-- sequential scan of the whole table. The index is on lower(buyer_email)
-- because this module, unlike customer and auth, has NO check constraint
-- forcing the stored address to lower case: an invoice copies what the document
-- said. Matching case-sensitively would let one capital letter answer "0 rows
-- retained" about a person whose invoice is sitting in the table.

CREATE INDEX invoices_buyer_email_idx ON invoices (lower(buyer_email));

-- The refusal for a row of invoices. The message names the id AND the number,
-- because the number is the thing an operator, an auditor or a data subject
-- recognizes the document by.
CREATE FUNCTION invoices_refuse_delete() RETURNS trigger LANGUAGE plpgsql AS
'BEGIN
    RAISE EXCEPTION ''invoice % (number %) is retained and cannot be deleted'',
        OLD.id, OLD.number
        USING ERRCODE = ''GB001'',
              DETAIL = ''ADR 0032: an issued invoice is a legal document, the erasure contract answers RETAINED for it, and deleting one would put a hole in a numbered series that must run without gaps.'',
              HINT = ''A deliberate administrative removal disables the triggers invoices_no_delete and invoice_lines_no_delete inside one transaction and re-enables them.'';
END;';

-- The refusal for a row of invoice_lines. It names the line and the document it
-- belongs to: a line id alone tells the operator nothing about what was nearly
-- taken apart.
CREATE FUNCTION invoice_lines_refuse_delete() RETURNS trigger LANGUAGE plpgsql AS
'BEGIN
    RAISE EXCEPTION ''line % of invoice % is retained and cannot be deleted'',
        OLD.id, OLD.invoice_id
        USING ERRCODE = ''GB001'',
              DETAIL = ''ADR 0032: the lines are part of the retained document, and removing them would leave the stored totals of an invoice standing against lines that no longer exist.'',
              HINT = ''A deliberate administrative removal disables the triggers invoices_no_delete and invoice_lines_no_delete inside one transaction and re-enables them.'';
END;';

-- The refusal for a TRUNCATE of either table. A statement trigger has no OLD,
-- so it names the table it fired for instead.
CREATE FUNCTION invoices_refuse_truncate() RETURNS trigger LANGUAGE plpgsql AS
'BEGIN
    RAISE EXCEPTION ''table % is retained and cannot be truncated'', TG_TABLE_NAME
        USING ERRCODE = ''GB001'',
              DETAIL = ''ADR 0032: an issued invoice is a legal document, and TRUNCATE would remove every document in the series at once without visiting a single row.'',
              HINT = ''A deliberate administrative removal disables the triggers invoices_no_truncate and invoice_lines_no_truncate inside one transaction and re-enables them.'';
END;';

CREATE TRIGGER invoices_no_delete
    BEFORE DELETE ON invoices
    FOR EACH ROW EXECUTE FUNCTION invoices_refuse_delete();

CREATE TRIGGER invoice_lines_no_delete
    BEFORE DELETE ON invoice_lines
    FOR EACH ROW EXECUTE FUNCTION invoice_lines_refuse_delete();

CREATE TRIGGER invoices_no_truncate
    BEFORE TRUNCATE ON invoices
    FOR EACH STATEMENT EXECUTE FUNCTION invoices_refuse_truncate();

CREATE TRIGGER invoice_lines_no_truncate
    BEFORE TRUNCATE ON invoice_lines
    FOR EACH STATEMENT EXECUTE FUNCTION invoices_refuse_truncate();

-- ENABLE ALWAYS is what makes the four refusals hold under
-- session_replication_role = 'replica'. Without it a single session-level SET
-- turns every trigger below off, and the refusal this migration exists for
-- becomes advisory. Measured both ways: in replica mode a plain trigger lets
-- the DELETE through and an ALWAYS trigger raises GB001.
ALTER TABLE invoices ENABLE ALWAYS TRIGGER invoices_no_delete;
ALTER TABLE invoice_lines ENABLE ALWAYS TRIGGER invoice_lines_no_delete;
ALTER TABLE invoices ENABLE ALWAYS TRIGGER invoices_no_truncate;
ALTER TABLE invoice_lines ENABLE ALWAYS TRIGGER invoice_lines_no_truncate;
