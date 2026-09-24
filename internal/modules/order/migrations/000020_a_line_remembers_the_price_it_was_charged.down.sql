-- Rolling back forgets which price each line was charged; the amounts stay.
ALTER TABLE order_line_items DROP CONSTRAINT IF EXISTS order_line_items_price_origin_coherent;
ALTER TABLE order_line_items
    DROP COLUMN IF EXISTS price_list_type,
    DROP COLUMN IF EXISTS price_list_id,
    DROP COLUMN IF EXISTS price_id;
