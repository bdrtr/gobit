-- Completion goes again and the replacement's source narrows back to a claim.
--
-- The replacements sourced from an exchange are DELETED rather than left behind:
-- the column they hang from is going, and a row whose only source is gone would
-- fail the NOT NULL this restores. They are also unreadable without it -- nothing
-- could say which order they belong to.
--
-- A completed exchange is put back to 'requested' rather than refused. Unlike
-- 000008's case this state IS reachable by code in this repository, so finding
-- one is not evidence of a hand-written row; what the rollback loses is the
-- moment, and the goods that left are still recorded by their replacement.
DELETE FROM order_replacements WHERE order_exchange_id IS NOT NULL;

ALTER TABLE order_exchanges
    DROP CONSTRAINT IF EXISTS order_exchanges_completed_owes_nothing,
    DROP CONSTRAINT IF EXISTS order_exchanges_completed_stamp;

UPDATE order_exchanges SET status = 'requested' WHERE status = 'completed';

ALTER TABLE order_exchanges
    DROP CONSTRAINT IF EXISTS order_exchanges_status_valid;

ALTER TABLE order_exchanges
    ADD CONSTRAINT order_exchanges_status_valid
        CHECK (status IN ('requested', 'canceled'));

ALTER TABLE order_exchanges
    DROP COLUMN IF EXISTS completed_at;

DROP INDEX IF EXISTS order_replacements_exchange_idx;

ALTER TABLE order_replacements
    DROP CONSTRAINT IF EXISTS order_replacements_one_source;

ALTER TABLE order_replacements
    DROP COLUMN IF EXISTS order_exchange_id;

ALTER TABLE order_replacements
    ALTER COLUMN order_claim_id SET NOT NULL;
