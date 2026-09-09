-- The rollback narrows the vocabulary and drops the two columns.
--
-- A row that is already 'dispatched' makes it FAIL, at the narrowed CHECK, and
-- that is the correct outcome: the goods left the warehouse and there is no
-- shape in the older schema that can say so. A migration that rewrote such a
-- row to 'requested' to make itself succeed would be inventing history.
ALTER TABLE order_replacements
    DROP CONSTRAINT IF EXISTS order_replacements_dispatched_names_its_parcel;

ALTER TABLE order_replacements
    DROP CONSTRAINT IF EXISTS order_replacements_dispatched_stamp;

ALTER TABLE order_replacements
    DROP CONSTRAINT IF EXISTS order_replacements_status_valid;

ALTER TABLE order_replacements
    ADD CONSTRAINT order_replacements_status_valid
        CHECK (status IN ('requested', 'canceled'));

ALTER TABLE order_replacements
    DROP COLUMN IF EXISTS fulfillment_id,
    DROP COLUMN IF EXISTS dispatched_at;

ALTER TABLE order_replacement_items
    DROP COLUMN IF EXISTS reservation_id;
