DROP INDEX IF EXISTS invoice_lines_amends_idx;
ALTER TABLE invoice_lines DROP COLUMN IF EXISTS amends_line_id;

DROP INDEX IF EXISTS invoices_amends_idx;
DROP INDEX IF EXISTS invoices_live_amendment_uniq;

ALTER TABLE invoices
    DROP CONSTRAINT IF EXISTS invoices_amendment_not_itself,
    DROP CONSTRAINT IF EXISTS invoices_amendment_key_not_blank,
    DROP CONSTRAINT IF EXISTS invoices_amendment_key_amends,
    DROP CONSTRAINT IF EXISTS invoices_amendment_reason_fits,
    DROP CONSTRAINT IF EXISTS invoices_amendment_reason_known,
    DROP CONSTRAINT IF EXISTS invoices_amendment_reason_named;

ALTER TABLE invoices
    DROP COLUMN IF EXISTS amendment_key,
    DROP COLUMN IF EXISTS amendment_reason,
    DROP COLUMN IF EXISTS amends_invoice_id;
