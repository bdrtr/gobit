-- The table goes with its index and its two retention triggers; the rows it
-- hangs from keep their own tax_rate_bps, which is the stack's base, so a
-- rolled-back schema still totals every document correctly and prints one rate
-- per row -- the state before this migration.
--
-- The three functions 000002 owns are put BACK to the text 000002 wrote. The up
-- leg replaced their hints to name a third guard; with that guard gone the hint
-- would send an operator to disable a trigger that no longer exists, and a
-- rollback that leaves a document behind saying something false about itself is
-- not a rollback.
DROP TRIGGER IF EXISTS invoice_line_taxes_no_truncate ON invoice_line_taxes;
DROP TRIGGER IF EXISTS invoice_line_taxes_no_delete ON invoice_line_taxes;
DROP INDEX IF EXISTS invoice_line_taxes_position_uniq;
DROP TABLE IF EXISTS invoice_line_taxes;
DROP FUNCTION IF EXISTS invoice_line_taxes_refuse_delete();

CREATE OR REPLACE FUNCTION invoices_refuse_delete() RETURNS trigger LANGUAGE plpgsql AS
'BEGIN
    RAISE EXCEPTION ''invoice % (number %) is retained and cannot be deleted'',
        OLD.id, OLD.number
        USING ERRCODE = ''GB001'',
              DETAIL = ''ADR 0032: an issued invoice is a legal document, the erasure contract answers RETAINED for it, and deleting one would put a hole in a numbered series that must run without gaps.'',
              HINT = ''A deliberate administrative removal disables the triggers invoices_no_delete and invoice_lines_no_delete inside one transaction and re-enables them.'';
END;';

CREATE OR REPLACE FUNCTION invoice_lines_refuse_delete() RETURNS trigger LANGUAGE plpgsql AS
'BEGIN
    RAISE EXCEPTION ''line % of invoice % is retained and cannot be deleted'',
        OLD.id, OLD.invoice_id
        USING ERRCODE = ''GB001'',
              DETAIL = ''ADR 0032: the lines are part of the retained document, and removing them would leave the stored totals of an invoice standing against lines that no longer exist.'',
              HINT = ''A deliberate administrative removal disables the triggers invoices_no_delete and invoice_lines_no_delete inside one transaction and re-enables them.'';
END;';

CREATE OR REPLACE FUNCTION invoices_refuse_truncate() RETURNS trigger LANGUAGE plpgsql AS
'BEGIN
    RAISE EXCEPTION ''table % is retained and cannot be truncated'', TG_TABLE_NAME
        USING ERRCODE = ''GB001'',
              DETAIL = ''ADR 0032: an issued invoice is a legal document, and TRUNCATE would remove every document in the series at once without visiting a single row.'',
              HINT = ''A deliberate administrative removal disables the triggers invoices_no_truncate and invoice_lines_no_truncate inside one transaction and re-enables them.'';
END;';
