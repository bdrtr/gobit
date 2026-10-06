DROP INDEX IF EXISTS invoices_amendment_voided_idx;
DROP INDEX IF EXISTS invoices_amendment_issued_idx;

ALTER TABLE invoices DROP CONSTRAINT IF EXISTS invoices_voided_when_final;

ALTER TABLE invoices DROP COLUMN IF EXISTS voided_at;
