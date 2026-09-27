-- The gift card lines have an index of their own (ADR 0212).
--
-- A scheduled sweep reads the gift card lines of the orders placed in the last
-- week, every five minutes, to issue the cards a lost delivery did not. Without
-- this index the read walks every line of the week's orders through
-- order_line_items_order_idx, or scans the whole table, to keep the few that
-- sold a card; measurements/0212 has the plans. The predicate keeps the index
-- as small as the gift card lines are few.
CREATE INDEX IF NOT EXISTS order_line_items_giftcard_idx
    ON order_line_items (order_id)
    WHERE is_giftcard;
