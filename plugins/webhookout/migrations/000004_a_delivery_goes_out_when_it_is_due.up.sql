-- A delivery goes out when it is due, and a receiver's cap counts per clock
-- minute (ADR 0436).
--
-- Every serving process now claims: its worker when a row is queued, redriven
-- or reaches the end of its backoff, and the minute job behind it. A
-- cap counted per claim would let each of them send a receiver its cap, so the
-- claim counts what it leases into the receiver's own row instead, under the
-- one lock every claim takes: rate_claimed is how many of its deliveries were
-- leased in the clock minute rate_window names, and a claim in a later minute
-- starts the count again. A lease that is released unattempted stays counted,
-- so the count can be under what was sent and never over it.
ALTER TABLE webhook_endpoint
    ADD COLUMN IF NOT EXISTS rate_window timestamptz,
    ADD COLUMN IF NOT EXISTS rate_claimed integer NOT NULL DEFAULT 0;

ALTER TABLE webhook_endpoint
    ADD CONSTRAINT webhook_endpoint_rate_claimed_nonneg CHECK (rate_claimed >= 0);

-- The worker asks when the next pending delivery falls due, and the claim asks
-- which are due now; both read next_attempt_at over the pending rows only.
CREATE INDEX IF NOT EXISTS webhook_delivery_next_attempt_idx
    ON webhook_delivery (next_attempt_at)
    WHERE delivered_at IS NULL AND dead_lettered_at IS NULL;
