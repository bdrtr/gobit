-- An order says whether its prices included their tax (ADR 0246, D169).
--
-- In a market whose prices include their tax, a line's subtotal is what is
-- left of unit_price x quantity once its tax is taken out (ADR 0086). The order
-- is the permanent answer to what was sold, and without the flag a reader could
-- not tell a line whose tax sits inside its unit price from one whose tax was
-- added on top. Every order before this column was placed with prices that did
-- not include their tax, since the cart refused to write any other, so false is
-- true of them.
ALTER TABLE orders
    ADD COLUMN IF NOT EXISTS prices_include_tax BOOLEAN NOT NULL DEFAULT false;
