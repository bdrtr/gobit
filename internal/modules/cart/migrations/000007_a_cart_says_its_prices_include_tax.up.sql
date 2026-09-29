-- A cart says whether its prices included their tax (ADR 0246, D169).
--
-- In a market whose prices include their tax, a line's subtotal is what is
-- left of unit_price x quantity once its tax is taken out (ADR 0086), and the
-- service held every line to unit_price x quantity, so no such cart could have
-- its totals written. The flag is written with the totals by the same UPDATE,
-- since it is what they were computed under, and a cart whose totals were never
-- written reads false, as every cart did before this column.
ALTER TABLE carts
    ADD COLUMN IF NOT EXISTS prices_include_tax BOOLEAN NOT NULL DEFAULT false;
