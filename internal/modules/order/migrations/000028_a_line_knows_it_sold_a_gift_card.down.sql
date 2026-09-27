-- Rolling back forgets which lines sold gift cards; the books then count them
-- as sales again.
ALTER TABLE order_line_items DROP COLUMN IF EXISTS is_giftcard;
