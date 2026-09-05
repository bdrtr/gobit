-- The column comes back EMPTY, and that costs nothing: it was never written, so
-- there is no value the rollback could have preserved.
--
-- The mirror CHECK goes first. It has to: it is the one constraint here that a
-- row can violate, and leaving it in place while the status vocabulary widens
-- again would let the schema forbid a state the CHECK below allows.
ALTER TABLE order_exchanges
    DROP CONSTRAINT IF EXISTS order_exchanges_canceled_stamp;

ALTER TABLE order_exchanges
    DROP CONSTRAINT IF EXISTS order_exchanges_status_valid;

ALTER TABLE order_exchanges
    ADD CONSTRAINT order_exchanges_status_valid
        CHECK (status IN ('requested', 'completed', 'canceled'));

ALTER TABLE order_exchanges
    ADD COLUMN IF NOT EXISTS completed_at TIMESTAMPTZ;
