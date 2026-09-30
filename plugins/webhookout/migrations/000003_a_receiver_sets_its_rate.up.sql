-- A receiver sets how many deliveries a minute it takes (ADR 0275).
--
-- The delivery job runs once a minute, so the rate is a cap on each pass: a
-- pass claims at most this many of the receiver's due deliveries, oldest
-- first, and the rest wait for the next pass without an attempt counted.
-- NULL takes whatever a pass can send.
ALTER TABLE webhook_endpoint
    ADD COLUMN IF NOT EXISTS max_per_minute integer;

ALTER TABLE webhook_endpoint
    ADD CONSTRAINT webhook_endpoint_rate_positive CHECK (max_per_minute IS NULL OR max_per_minute > 0);
