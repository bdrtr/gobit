-- Rolling back forgets which order a credit compensates. The credit and its
-- balance stay as they are, so nothing stops the rollback.
DROP INDEX IF EXISTS payment_store_credit_entries_order_idx;

ALTER TABLE payment_store_credit_entries
    DROP CONSTRAINT IF EXISTS payment_store_credit_entries_order_on_issue,
    DROP COLUMN IF EXISTS order_id;
