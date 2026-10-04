-- An order confirmation is pushed at most once (ADR 0389, D236).
--
-- The outbox delivers order.placed twice under one id, once from the publish
-- after the commit and once from the relay, and the bus deduplicates nothing.
-- The order.placed handler writes the event's id here before it pushes, and a
-- delivery whose id is already here pushes nothing. The primary key decides
-- between two deliveries that arrive together: the second INSERT waits for the
-- first and then does nothing.
--
-- A row says a fan-out was claimed, about to start, not that a device received
-- anything, so it carries no status and nothing resends from it. It holds an event id and a
-- moment, no customer and no device.
--
-- The handler pushes no order placed more than four hours before (the push's
-- TTL), so a row only has to outlive that; the handler that writes a row
-- deletes the ones older than a day.
CREATE TABLE IF NOT EXISTS webpush_claimed_event (
    event_id text PRIMARY KEY,
    claimed_at timestamptz NOT NULL DEFAULT now(),

    -- An event without an id cannot be told from its second delivery. The
    -- handler refuses an empty or blank one before it gets here; the database
    -- refuses it as well.
    CONSTRAINT webpush_claimed_event_id_not_blank CHECK (length(btrim(event_id)) > 0)
);

-- The handler deletes by age after each push.
CREATE INDEX IF NOT EXISTS webpush_claimed_event_claimed_at_idx
    ON webpush_claimed_event (claimed_at);
