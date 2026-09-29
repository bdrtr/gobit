-- An invoice says whether its prices included their tax (ADR 0248).
--
-- Where they did, a row's unit price is the sticker and its subtotal is what is
-- left of unit_price x quantity once the row's tax is taken out (ADR 0246). A
-- transmission provider reading the rows could not tell the two apart without
-- this column. Every document before it was issued for an order whose prices did
-- not include their tax, since no such order could be placed, so false is true
-- of them; the column is added, not written, so no issued amount changes.
ALTER TABLE invoices
    ADD COLUMN IF NOT EXISTS prices_include_tax BOOLEAN NOT NULL DEFAULT false;
