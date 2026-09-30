-- Rolling back forgets every receiver's rate; each is then sent whatever a
-- pass can send, which is how every receiver was sent before.
ALTER TABLE webhook_endpoint
    DROP CONSTRAINT IF EXISTS webhook_endpoint_rate_positive,
    DROP COLUMN IF EXISTS max_per_minute;
