-- A replacement can be SENT, and the row says so.
--
-- # What this adds and why now
--
-- 000011 gave a claim a way to say what to send and stopped there, with a
-- deliberate two-word vocabulary: "the vocabulary says what can happen, and
-- today what can happen is that a replacement is asked for and withdrawn". The
-- flow that moves the goods arrives with this migration, so the third word
-- arrives with it — and only the third. There is still no 'held' and no
-- 'dispatching', because nothing waits and nothing is half-sent: the dispatch
-- sets stock aside, opens a parcel and confirms, and what is written here is
-- its outcome.
--
-- # Two identifiers that belong to other modules
--
-- fulfillment_id is the parcel and lives in the fulfillment module;
-- order_replacement_items.reservation_id is the promise the units are held
-- under and lives in inventory. Neither carries a foreign key (Principle 2.2),
-- and both are written by the flow that holds both sides.
--
-- The reservation id is what makes the dispatch RETRYABLE. A retry that found
-- no record of the earlier promise would set the same units aside a second
-- time; with the id on the row, a repeated dispatch reuses the promise it
-- already made and the confirm behind it is idempotent.
ALTER TABLE order_replacements
    ADD COLUMN IF NOT EXISTS dispatched_at  TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS fulfillment_id TEXT;

ALTER TABLE order_replacement_items
    ADD COLUMN IF NOT EXISTS reservation_id TEXT;

ALTER TABLE order_replacements
    DROP CONSTRAINT IF EXISTS order_replacements_status_valid;

ALTER TABLE order_replacements
    ADD CONSTRAINT order_replacements_status_valid
        CHECK (status IN ('requested', 'canceled', 'dispatched'));

-- The mirror form, the same shape 000011 gave the withdrawal: the status and
-- its moment imply each other in BOTH directions, so a row cannot say goods
-- went out without saying when, and cannot carry a departure moment while
-- claiming to be waiting.
ALTER TABLE order_replacements
    ADD CONSTRAINT order_replacements_dispatched_stamp
        CHECK ((status = 'dispatched') = (dispatched_at IS NOT NULL));

-- A parcel is what makes a dispatch a dispatch. The row is stamped AFTER the
-- shipment exists, so a dispatched replacement that named no parcel would be a
-- record of goods leaving with nothing to carry them.
ALTER TABLE order_replacements
    ADD CONSTRAINT order_replacements_dispatched_names_its_parcel
        CHECK (status <> 'dispatched' OR fulfillment_id IS NOT NULL);
