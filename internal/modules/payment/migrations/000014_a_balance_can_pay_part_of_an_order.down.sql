-- Rolling back forgets which sessions asked for a partial hold. One still
-- pending is then authorized whole or declined, which is a refusal and not a
-- wrong movement, so nothing stops the rollback.
ALTER TABLE payment_loyalty_sessions
    DROP COLUMN IF EXISTS partial;

ALTER TABLE payment_store_credit_sessions
    DROP COLUMN IF EXISTS partial;
