-- A store credit can name the order it compensates (ADR 0274).
--
-- Only an issue names one: a hold, a release, a refund or an expiry is money
-- moving within the balance, and the order each of those belongs to is the
-- session's, not the credit's. The order is another module's record and there
-- is no foreign key (Principle 2.2); the CHECK holds the shape, one line so the
-- personal-data audit's parser reads it whole.
ALTER TABLE payment_store_credit_entries
    ADD COLUMN IF NOT EXISTS order_id TEXT;

ALTER TABLE payment_store_credit_entries
    ADD CONSTRAINT payment_store_credit_entries_order_on_issue CHECK (order_id IS NULL OR (kind = 'issue' AND length(btrim(order_id)) > 0));

-- The credits issued for one order, newest first.
CREATE INDEX IF NOT EXISTS payment_store_credit_entries_order_idx
    ON payment_store_credit_entries (order_id, created_at)
    WHERE order_id IS NOT NULL;
