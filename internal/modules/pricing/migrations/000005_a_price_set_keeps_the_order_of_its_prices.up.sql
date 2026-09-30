-- A price set keeps the order of its prices, and a price the order of its rules
-- (D175).
--
-- A set's prices and their rules are written in one transaction with one stamp,
-- and their ids end in random bits: ordering by the id listed them in an order
-- nobody wrote on the admin and storefront reads, and the history recorded that
-- order in its snapshots. seq is the order the database took the rows in, which
-- is the order the write loop gave them, the rule ADR 0233 gave an order's
-- lines; the rows already there are numbered in whatever order the table held
-- them.
ALTER TABLE price
    ADD COLUMN IF NOT EXISTS seq BIGINT GENERATED ALWAYS AS IDENTITY;
ALTER TABLE price_rule
    ADD COLUMN IF NOT EXISTS seq BIGINT GENERATED ALWAYS AS IDENTITY;
