-- Rolling 000003 back: the four columns and the eight partial indexes return,
-- under the names 000001 and 000002 gave them.
--
-- The rows come back with deleted_at NULL, the value every row had while the
-- columns existed -- nothing ever wrote them, so nothing is lost by restoring
-- them empty.
--
-- The indexes are dropped and rebuilt rather than left alone. They exist here
-- with WIDER predicates than the originals (the two uniqueness rules now cover
-- every row, because no row can be retired), and a down that left them would
-- leave the schema one step short of where 000001 put it while looking
-- finished. The next up would then skip them because of IF NOT EXISTS and the
-- difference would stand for ever.
ALTER TABLE payment_collections ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;
ALTER TABLE payment_sessions    ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;
ALTER TABLE payments            ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;
ALTER TABLE refunds             ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;

DROP INDEX IF EXISTS payment_collections_reference_idx;
DROP INDEX IF EXISTS payment_collections_listing_idx;
DROP INDEX IF EXISTS payment_sessions_provider_idempotency_uniq;
DROP INDEX IF EXISTS payment_sessions_collection_idx;
DROP INDEX IF EXISTS payments_session_uniq;
DROP INDEX IF EXISTS payments_collection_idx;
DROP INDEX IF EXISTS refunds_payment_idx;
DROP INDEX IF EXISTS payment_sessions_reconcile_idx;

CREATE INDEX IF NOT EXISTS payment_collections_reference_idx
    ON payment_collections (reference)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS payment_collections_alive_idx
    ON payment_collections (created_at DESC, id DESC)
    WHERE deleted_at IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS payment_sessions_provider_idempotency_uniq
    ON payment_sessions (provider_id, idempotency_key)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS payment_sessions_collection_idx
    ON payment_sessions (payment_collection_id, created_at DESC)
    WHERE deleted_at IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS payments_session_uniq
    ON payments (payment_session_id)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS payments_collection_idx
    ON payments (payment_collection_id, created_at DESC)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS refunds_payment_idx
    ON refunds (payment_id, created_at DESC)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS payment_sessions_reconcile_idx
    ON payment_sessions (updated_at)
    WHERE status = 'authorized' AND deleted_at IS NULL;
