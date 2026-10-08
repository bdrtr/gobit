-- Rolling back takes away the reason an exchange's sale carries and the two
-- kinds of act an exchange's documents name, and a database holding a
-- document of an exchange refuses it: the CHECK below fails on that row first,
-- before anything is dropped. Without them the order journal could not place
-- the document's tax and would refuse every read of its window (ADR 0419),
-- and an issued document is retained (000002), so it cannot be removed to
-- make room either; a canceled one still names its act for the window it was
-- voided in.
ALTER TABLE invoices
    ADD CONSTRAINT invoices_carries_no_exchange
        CHECK (amendment_key IS NULL
               OR split_part(amendment_key, ':', 1) NOT IN ('exchange_returned', 'exchange_sent'));
ALTER TABLE invoices DROP CONSTRAINT IF EXISTS invoices_carries_no_exchange;

ALTER TABLE invoices
    DROP CONSTRAINT IF EXISTS invoices_amendment_reason_fits,
    DROP CONSTRAINT IF EXISTS invoices_amendment_reason_known;

ALTER TABLE invoices
    ADD CONSTRAINT invoices_amendment_reason_known
        CHECK (amendment_reason IS NULL OR amendment_reason IN ('returned', 'price_lowered', 'price_raised')),
    ADD CONSTRAINT invoices_amendment_reason_fits
        CHECK (amendment_reason IS NULL OR (amendment_reason = 'price_raised') = (kind = 'sale'));
