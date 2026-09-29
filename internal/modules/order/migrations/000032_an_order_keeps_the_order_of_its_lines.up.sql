-- An order keeps the order of its lines (ADR 0233, D157).
--
-- The lines of an order are written in one transaction, so they share a
-- created_at, and their ids end in random bits, so ordering by the two listed
-- them in an order nobody wrote. seq is the order the database took the rows
-- in, which is the order the write loop gave them; the rows already there are
-- numbered in whatever order the table held them.
ALTER TABLE order_line_items
    ADD COLUMN IF NOT EXISTS seq BIGINT GENERATED ALWAYS AS IDENTITY;
