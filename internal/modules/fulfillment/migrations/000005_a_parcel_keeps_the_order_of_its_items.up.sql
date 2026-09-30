-- A parcel keeps the order of its items (D175).
--
-- A parcel's items are written in one transaction, so they share a created_at,
-- and their ids end in random bits: ordering by the id listed them in an order
-- nobody wrote, on the parcel's own read, in a list and in an idempotent
-- replay. seq is the order the database took the rows in, which is the order
-- the write loop gave them, the rule ADR 0233 gave an order's lines; the rows
-- already there are numbered in whatever order the table held them.
ALTER TABLE fulfillment_items
    ADD COLUMN IF NOT EXISTS seq BIGINT GENERATED ALWAYS AS IDENTITY;
