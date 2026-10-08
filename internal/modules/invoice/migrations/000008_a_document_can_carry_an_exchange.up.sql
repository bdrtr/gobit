-- A document can carry an exchange (ADR 0432).
--
-- An exchange that names the return taking its goods back is documented as
-- two documents amending the order's sale: a refund of the units the return
-- takes back, which is a return, and a sale adding a row for each item the
-- exchange sends. The second is neither a price raised nor a return, and a
-- provider transmits it as a sale of new goods, so it carries a reason of its
-- own, `exchanged`, which fits a sale only.
--
-- # What the CHECKs hold
--
-- The two constraints 000006 wrote are replaced under the same names: the
-- reason is one of four, and `price_raised` and `exchanged` are a sale's while
-- `returned` and `price_lowered` are a refund's. Each answers TRUE or FALSE for
-- every NULL combination (ADR 0169): a document that amends nothing has no
-- reason, and `kind` is NOT NULL.
ALTER TABLE invoices
    DROP CONSTRAINT IF EXISTS invoices_amendment_reason_fits,
    DROP CONSTRAINT IF EXISTS invoices_amendment_reason_known;

ALTER TABLE invoices
    ADD CONSTRAINT invoices_amendment_reason_known
        CHECK (amendment_reason IS NULL
               OR amendment_reason IN ('returned', 'price_lowered', 'price_raised', 'exchanged')),
    ADD CONSTRAINT invoices_amendment_reason_fits
        CHECK (amendment_reason IS NULL
               OR (amendment_reason IN ('price_raised', 'exchanged')) = (kind = 'sale'));
