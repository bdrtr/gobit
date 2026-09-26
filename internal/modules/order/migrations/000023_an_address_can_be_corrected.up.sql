-- A shipping address can be corrected before anything ships (ADR 0195).
--
-- A correction does not edit the row the order was placed with. It closes it,
-- by stamping superseded_at, and writes the corrected address as a new row in
-- the same transaction. The order's current address is the row of its type
-- that nothing has superseded; the others are what the order held before, and
-- they stay: the person's file lists them, the erasure empties them with the
-- rest, and the timeline dates each correction by them.
ALTER TABLE order_addresses
    ADD COLUMN IF NOT EXISTS superseded_at TIMESTAMPTZ;

-- A row cannot be superseded before it was written. The IS NULL arm is not
-- decoration: a comparison with NULL answers NULL, and a CHECK that answers
-- NULL passes the row (ADR 0169).
ALTER TABLE order_addresses
    ADD CONSTRAINT order_addresses_superseded_after_written
        CHECK (superseded_at IS NULL OR superseded_at >= created_at);

-- ONE CURRENT address of each type per order. 000005's rule was one per type
-- outright, which a history of corrections cannot keep; what it protected — a
-- parcel with two destinations — is the current pair, and that is what the
-- partial index still refuses.
DROP INDEX IF EXISTS order_addresses_one_per_type;

CREATE UNIQUE INDEX IF NOT EXISTS order_addresses_one_current_per_type
    ON order_addresses (order_id, address_type)
    WHERE superseded_at IS NULL;
