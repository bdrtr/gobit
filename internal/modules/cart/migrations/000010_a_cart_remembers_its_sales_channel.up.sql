-- A cart remembers the sales channel it was opened in, the one its prices and
-- promotions are asked in (ADR 0397). NULL is a cart opened before this, by a
-- key bound to several channels or to none, or by an operator who named none.
-- This module does not read the auth module's tables.
ALTER TABLE carts
    ADD COLUMN IF NOT EXISTS sales_channel_id TEXT;
ALTER TABLE carts
    ADD CONSTRAINT carts_sales_channel_id_not_blank
    CHECK (sales_channel_id IS NULL OR length(btrim(sales_channel_id)) > 0);
