-- The open carts are read by their last change, the oldest first, so the
-- retention job deletes the ones untouched for the shop's period without
-- sorting the whole table (ADR 0301, measurements/0301).
CREATE INDEX IF NOT EXISTS carts_abandoned_idx
    ON carts (updated_at, id)
    WHERE completed_at IS NULL;
