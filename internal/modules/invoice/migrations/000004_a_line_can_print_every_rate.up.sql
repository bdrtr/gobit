-- invoice_line_taxes is the per-rate breakdown of a row taxed by a STACK.
--
-- # Why the row's own rate was not enough
--
-- 000001 put tax_rate_bps on the row and argued that a document prints the rate
-- that was CHARGED rather than one recomputed from a rounded amount. That
-- argument holds and this table is its last half: since ADR 0095 a line can be
-- taxed by several rates at once, the order records every one of them
-- (ADR 0096), and until now the document dropped all but the first. A row taxed
-- at 5% + 8% printed "5%" -- a rate really applied on an amount really
-- recorded, and still not what the buyer paid under.
--
-- In Turkey the KDV rate is a required field on an e-fatura, not a derived one.
-- A document that states one rate for a row charged under two states something
-- the buyer's own arithmetic contradicts.
--
-- # Why rows and not a JSONB column
--
-- The constraints, for the same reason the order's table gives (its migration
-- 000013): a rate outside [0, 10000], a component taking more than its own
-- base and two components in the same place are each a CHECK here and none of
-- them is expressible over a JSON document.
--
-- The sum identity -- the components add up to the row's tax_total -- spans
-- rows, so no CHECK can hold it and the service does. It is checked here as
-- well as at the three boundaries before it, because this is the last one: what
-- passes here is printed.
--
-- # Why position starts at 1 and the order's starts at 0
--
-- Because invoice_lines.position starts at 1. Position in this module is the
-- PRINTED order and a document's first row is row 1; a sibling table counting
-- from 0 would make one reader of one module answer "which is first" two ways.
-- The order module's table counts from 0 for the same kind of reason -- nothing
-- there is printed -- and the conversion happens where the two meet.
--
-- # Why there is no row for a single-rate line
--
-- A breakdown of one says nothing the row does not, and its absence is what
-- lets a renderer take "this row has components" to mean "print the breakdown".
CREATE TABLE invoice_line_taxes (
    id              text    PRIMARY KEY,
    invoice_line_id text    NOT NULL REFERENCES invoice_lines (id) ON DELETE CASCADE,
    -- position is the printed order of the component within the row, the base
    -- first, from 1.
    position        integer NOT NULL,
    -- rate_id is the tax module's id; there is NO FK (Principle 2.2). It is
    -- empty when an external provider carries no ids of its own.
    rate_id         text    NOT NULL DEFAULT '',
    rate_bps        integer NOT NULL,
    -- compound says the component was computed on the row's amount PLUS the
    -- taxes below it.
    compound        boolean NOT NULL DEFAULT FALSE,
    taxable_amount  bigint  NOT NULL,
    tax_amount      bigint  NOT NULL,

    CONSTRAINT invoice_line_taxes_position_positive CHECK (position >= 1),
    CONSTRAINT invoice_line_taxes_rate_bps_range
        CHECK (rate_bps >= 0 AND rate_bps <= 10000),
    CONSTRAINT invoice_line_taxes_taxable_nonneg CHECK (taxable_amount >= 0),
    -- The same bound the row carries, one component at a time: a rate is at
    -- most 100%, so a component can never take more than the base it was
    -- computed on.
    CONSTRAINT invoice_line_taxes_within_base
        CHECK (tax_amount >= 0 AND tax_amount <= taxable_amount),
    -- The first component stands on nothing, so it cannot compound.
    CONSTRAINT invoice_line_taxes_compound_needs_base
        CHECK (position > 1 OR compound = FALSE)
);

-- The position is unique per row: two components in the same place would leave
-- the printed order ambiguous, and a compound component's base is defined by
-- what is below it.
CREATE UNIQUE INDEX invoice_line_taxes_position_uniq
    ON invoice_line_taxes (invoice_line_id, position);

-- # The breakdown is part of the retained document, so it is guarded like one
--
-- 000002 measured this exact hole one table earlier: with the invoices trigger
-- in place and nothing on the child, "DELETE FROM invoice_lines WHERE
-- invoice_id = ..." still reported DELETE 1, and it wrote that "a guard on the
-- parent alone protects the number and loses the document". This table is the
-- THIRD holding part of the document, and unguarded it would have reopened the
-- same hole a level down: the row would keep its tax_total standing against
-- components that no longer exist, which is the state 000001 stores the totals
-- to prevent.
--
-- The shape is 000002's, unchanged and for its reasons: a BEFORE DELETE row
-- trigger so a DELETE matching nothing stays a harmless DELETE 0, a BEFORE
-- TRUNCATE statement companion because TRUNCATE visits no rows, SQLSTATE GB001
-- so an embedder can tell the refusal from a real constraint failure, and
-- ENABLE ALWAYS so a session-level session_replication_role cannot silence it.
-- The bodies are single-quoted with doubled quotes and carry no double-hyphen,
-- because the SQL audits in internal/arch blank comments before literals.
--
-- # The sanctioned escape is now THREE pairs, and the hints say so
--
-- 000002 spelled the escape as two DISABLE statements and two to put back, and
-- its three functions name the two triggers by name. With a third guarded table
-- that text is no longer true, so the three functions are REPLACED here with
-- hints that name all three. Replacing them rather than leaving them stale is
-- the point: the hint is what an operator reads at the moment they are about to
-- take a document apart, and a hint that lists two of three guards sends them
-- into a transaction that fails halfway.
--
-- The down leg puts the original three back, so a rollback leaves 000002 saying
-- exactly what it said before this migration ran.
CREATE FUNCTION invoice_line_taxes_refuse_delete() RETURNS trigger LANGUAGE plpgsql AS
'BEGIN
    RAISE EXCEPTION ''tax component % of invoice line % is retained and cannot be deleted'',
        OLD.id, OLD.invoice_line_id
        USING ERRCODE = ''GB001'',
              DETAIL = ''ADR 0032 and ADR 0097: the per-rate breakdown is part of the retained document, and removing it would leave a row printing one rate for a tax charged under several.'',
              HINT = ''A deliberate administrative removal disables the triggers invoices_no_delete, invoice_lines_no_delete and invoice_line_taxes_no_delete inside one transaction and re-enables them.'';
END;';

CREATE TRIGGER invoice_line_taxes_no_delete
    BEFORE DELETE ON invoice_line_taxes
    FOR EACH ROW EXECUTE FUNCTION invoice_line_taxes_refuse_delete();

CREATE TRIGGER invoice_line_taxes_no_truncate
    BEFORE TRUNCATE ON invoice_line_taxes
    FOR EACH STATEMENT EXECUTE FUNCTION invoices_refuse_truncate();

ALTER TABLE invoice_line_taxes ENABLE ALWAYS TRIGGER invoice_line_taxes_no_delete;
ALTER TABLE invoice_line_taxes ENABLE ALWAYS TRIGGER invoice_line_taxes_no_truncate;

CREATE OR REPLACE FUNCTION invoices_refuse_delete() RETURNS trigger LANGUAGE plpgsql AS
'BEGIN
    RAISE EXCEPTION ''invoice % (number %) is retained and cannot be deleted'',
        OLD.id, OLD.number
        USING ERRCODE = ''GB001'',
              DETAIL = ''ADR 0032: an issued invoice is a legal document, the erasure contract answers RETAINED for it, and deleting one would put a hole in a numbered series that must run without gaps.'',
              HINT = ''A deliberate administrative removal disables the triggers invoices_no_delete, invoice_lines_no_delete and invoice_line_taxes_no_delete inside one transaction and re-enables them.'';
END;';

CREATE OR REPLACE FUNCTION invoice_lines_refuse_delete() RETURNS trigger LANGUAGE plpgsql AS
'BEGIN
    RAISE EXCEPTION ''line % of invoice % is retained and cannot be deleted'',
        OLD.id, OLD.invoice_id
        USING ERRCODE = ''GB001'',
              DETAIL = ''ADR 0032: the lines are part of the retained document, and removing them would leave the stored totals of an invoice standing against lines that no longer exist.'',
              HINT = ''A deliberate administrative removal disables the triggers invoices_no_delete, invoice_lines_no_delete and invoice_line_taxes_no_delete inside one transaction and re-enables them.'';
END;';

CREATE OR REPLACE FUNCTION invoices_refuse_truncate() RETURNS trigger LANGUAGE plpgsql AS
'BEGIN
    RAISE EXCEPTION ''table % is retained and cannot be truncated'', TG_TABLE_NAME
        USING ERRCODE = ''GB001'',
              DETAIL = ''ADR 0032: an issued invoice is a legal document, and TRUNCATE would remove every document in the series at once without visiting a single row.'',
              HINT = ''A deliberate administrative removal disables the triggers invoices_no_truncate, invoice_lines_no_truncate and invoice_line_taxes_no_truncate inside one transaction and re-enables them.'';
END;';
