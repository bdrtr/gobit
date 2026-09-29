-- A cart keeps the order of its lines (ADR 0233, D157).
--
-- A line and its add-ons (ADR 0229), and the lines a merge opens, are written
-- in one transaction and share a created_at; their ids end in random bits, so
-- ordering by the two listed them in an order nobody wrote. seq is the order
-- the database took the rows in.
ALTER TABLE cart_line_items
    ADD COLUMN IF NOT EXISTS seq BIGINT GENERATED ALWAYS AS IDENTITY;
