-- An invoice can amend a sale document (ADR 0406).
--
-- A credit, a delivery change and a return's or a claim's refund move money on
-- an order after its sale document was issued. Each is documented by a further
-- document naming the sale it amends and, row by row, the sale row each of its
-- rows moves: a refund gives back part of a row, a sale charges more on one.
--
-- # What the columns say
--
-- amends_invoice_id names the sale document, amendment_reason says why
-- (returned, price_lowered or price_raised; a provider transmits a return and a
-- price difference as different documents), and amendment_key names the act the
-- invoicing flow documented, so an act is documented once.
-- invoice_lines.amends_line_id names the sale row a row moves.
--
-- # What the database holds and what Go holds
--
-- The constraints below hold each document to itself. There is NO CHECK that a
-- refund names a sale: refunds issued before this file name none, and a CHECK
-- would make a status UPDATE on one of them fail. The service refuses a new
-- refund that names none. That a row's amends_line_id is a row of the amended
-- document, and that a row gives back no more than it carried, span documents;
-- the service checks both with the sale locked.
--
-- # One live document per act
--
-- invoices_live_amendment_uniq holds an act to one LIVE document. A rejected or
-- canceled amendment frees its act, since what follows a rejection is a new
-- document, and the number it spent stays spent.
--
-- # Removal
--
-- 000002's sanctioned removal now has an order: amends_invoice_id and
-- amends_line_id reference the sale, so an amending document is removed before
-- the sale it amends.
ALTER TABLE invoices
    ADD COLUMN IF NOT EXISTS amends_invoice_id text REFERENCES invoices (id),
    ADD COLUMN IF NOT EXISTS amendment_reason  text,
    ADD COLUMN IF NOT EXISTS amendment_key     text;

ALTER TABLE invoices
    ADD CONSTRAINT invoices_amendment_reason_named
        CHECK ((amends_invoice_id IS NULL) = (amendment_reason IS NULL)),
    ADD CONSTRAINT invoices_amendment_reason_known
        CHECK (amendment_reason IS NULL OR amendment_reason IN ('returned', 'price_lowered', 'price_raised')),
    ADD CONSTRAINT invoices_amendment_reason_fits
        CHECK (amendment_reason IS NULL OR (amendment_reason = 'price_raised') = (kind = 'sale')),
    ADD CONSTRAINT invoices_amendment_key_amends
        CHECK (amendment_key IS NULL OR amends_invoice_id IS NOT NULL),
    ADD CONSTRAINT invoices_amendment_key_not_blank
        CHECK (amendment_key IS NULL OR length(btrim(amendment_key)) > 0),
    ADD CONSTRAINT invoices_amendment_not_itself
        CHECK (amends_invoice_id IS NULL OR amends_invoice_id <> id);

CREATE UNIQUE INDEX IF NOT EXISTS invoices_live_amendment_uniq
    ON invoices (amends_invoice_id, amendment_key)
    WHERE amendment_key IS NOT NULL AND status IN ('issued', 'sent', 'accepted');

CREATE INDEX IF NOT EXISTS invoices_amends_idx
    ON invoices (amends_invoice_id) WHERE amends_invoice_id IS NOT NULL;

ALTER TABLE invoice_lines
    ADD COLUMN IF NOT EXISTS amends_line_id text REFERENCES invoice_lines (id);

CREATE INDEX IF NOT EXISTS invoice_lines_amends_idx
    ON invoice_lines (amends_line_id) WHERE amends_line_id IS NOT NULL;
