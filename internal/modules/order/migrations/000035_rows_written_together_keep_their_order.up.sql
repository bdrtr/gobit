-- Rows an order writes together keep their order (D161).
--
-- A return's lines, a replacement's lines, a line's cancellation with its
-- add-ons' and an order's deliveries are each written in one transaction, so
-- they share a created_at, and their ids end in random bits: ordering by the
-- two listed them in an order nobody wrote, and a replacement's dispatch set its
-- lines aside in that order. seq is the order the database took the rows in,
-- which is the order the write loop gave them, the rule ADR 0233 gave an
-- order's lines; the rows already there are numbered in whatever order the
-- table held them.
ALTER TABLE order_return_items
    ADD COLUMN IF NOT EXISTS seq BIGINT GENERATED ALWAYS AS IDENTITY;
ALTER TABLE order_replacement_items
    ADD COLUMN IF NOT EXISTS seq BIGINT GENERATED ALWAYS AS IDENTITY;
ALTER TABLE order_line_cancellations
    ADD COLUMN IF NOT EXISTS seq BIGINT GENERATED ALWAYS AS IDENTITY;
ALTER TABLE order_shipping_methods
    ADD COLUMN IF NOT EXISTS seq BIGINT GENERATED ALWAYS AS IDENTITY;
