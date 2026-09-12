-- Rolling back takes the credit ledger with it.
--
-- That is the honest loss and it is why a rollback past this point is an
-- operator's decision rather than a routine step: the rows are MONEY the shop
-- owes its customers, and no other table holds them.
DROP TABLE IF EXISTS payment_store_credit_sessions;

DROP TABLE IF EXISTS payment_store_credit_entries;

ALTER TABLE payment_collections
    DROP CONSTRAINT IF EXISTS payment_collections_customer_not_blank;

ALTER TABLE payment_collections
    DROP COLUMN IF EXISTS customer_id;
