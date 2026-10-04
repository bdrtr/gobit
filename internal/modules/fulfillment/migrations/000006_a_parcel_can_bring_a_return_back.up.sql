-- A parcel can bring a return back (ADR 0384, D234).
--
-- 000001 and 000004 said goods a customer sends back travel in a second
-- fulfillment on an is_return option. The dispatch bound (ADR 0135) refused that
-- parcel for units already shipped, and nothing said which return it served.
-- return_id is the order module's return identifier, not a foreign key
-- (Principle 2.2), for the reason reference is not one in 000001. A parcel
-- opened before this migration names no return and stays what it was.
ALTER TABLE fulfillments ADD COLUMN IF NOT EXISTS return_id TEXT;
ALTER TABLE fulfillments ADD CONSTRAINT fulfillments_return_id_check
    CHECK (return_id IS NULL OR return_id <> '');
CREATE INDEX IF NOT EXISTS fulfillments_return_idx
    ON fulfillments (return_id) WHERE return_id IS NOT NULL;
