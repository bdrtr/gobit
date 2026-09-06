-- Rolling 000003 back: the column and the three partial indexes of 000001
-- return, under their original names.
--
-- The rows come back with deleted_at NULL, the value every row had while the
-- column existed — nothing ever wrote it, so nothing is lost by restoring it
-- empty.
--
-- The indexes are dropped and rebuilt rather than left alone. They exist here
-- with WIDER predicates than 000001's (the uniqueness now covers deleted rows
-- too, because there are none), and a down that left them would leave the
-- schema one step short of where 000001 put it while looking finished. The next
-- up would then skip them because of IF NOT EXISTS and the difference would
-- stand forever.
ALTER TABLE fulfillments ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;

DROP INDEX IF EXISTS fulfillments_idempotency_uniq;
DROP INDEX IF EXISTS fulfillments_reference_idx;
DROP INDEX IF EXISTS fulfillments_listing_idx;

CREATE UNIQUE INDEX IF NOT EXISTS fulfillments_idempotency_uniq
    ON fulfillments (idempotency_key)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS fulfillments_reference_idx
    ON fulfillments (reference, created_at DESC)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS fulfillments_alive_idx
    ON fulfillments (created_at DESC, id DESC)
    WHERE deleted_at IS NULL;
