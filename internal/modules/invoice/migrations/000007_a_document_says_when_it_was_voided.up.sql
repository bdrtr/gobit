-- A document says when it was voided (ADR 0419).
--
-- The order journal books the tax an amending document gives back or charges
-- at the document's issued_at, and takes it back at the moment the document is
-- rejected or canceled (ADR 0419). Rejected and canceled are final, so a
-- document is voided once, and voided_at is that moment.
--
-- # Why updated_at is exact for the documents voided before this file
--
-- SetInvoiceStatus is the one statement that moves updated_at: the erasure
-- handle's rewrite leaves it as it was on purpose (queries/invoice.sql), and an
-- issued document takes no other UPDATE. A rejected or canceled document cannot
-- move again (models.Status.CanMoveTo), so its updated_at is the moment it was
-- voided, to the statement.
--
-- # What the CHECK holds
--
-- A live document carries no voided_at and a voided one carries one: the
-- journal would otherwise book a correction back for a document still
-- standing, or never take back one that fell.
ALTER TABLE invoices ADD COLUMN IF NOT EXISTS voided_at timestamptz;

UPDATE invoices SET voided_at = updated_at
WHERE status IN ('rejected', 'canceled') AND voided_at IS NULL;

ALTER TABLE invoices
    ADD CONSTRAINT invoices_voided_when_final
        CHECK ((voided_at IS NOT NULL) = (status IN ('rejected', 'canceled')));

-- The journal's two reads of the amending documents, by the moment each was
-- issued and by the moment each was voided.
CREATE INDEX IF NOT EXISTS invoices_amendment_issued_idx
    ON invoices (issued_at, id) WHERE amendment_key IS NOT NULL;

CREATE INDEX IF NOT EXISTS invoices_amendment_voided_idx
    ON invoices (voided_at, id) WHERE amendment_key IS NOT NULL AND voided_at IS NOT NULL;
