-- Rolling back forgets the per-minute counts; a capped receiver's cap is then
-- counted per claim again, which is what it was while one job claimed once a
-- minute, and code that sends between the job's passes would exceed it.
DROP INDEX IF EXISTS webhook_delivery_next_attempt_idx;

ALTER TABLE webhook_endpoint
    DROP CONSTRAINT IF EXISTS webhook_endpoint_rate_claimed_nonneg,
    DROP COLUMN IF EXISTS rate_claimed,
    DROP COLUMN IF EXISTS rate_window;
