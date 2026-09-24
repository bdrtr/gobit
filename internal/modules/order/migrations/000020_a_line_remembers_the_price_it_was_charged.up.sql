-- A line remembers the price it was charged (ADR 0168).
--
-- The unit price was already on the line; which of the variant's prices it
-- came from was not. Pricing's ladder picks one price row among a set's base
-- prices, sale and override lists, quantity tiers and rules, and used to hand
-- the checkout the amount alone. The line now keeps the row's id and, when the
-- price came from a list, the list and its type — and with pricing's history
-- (ADR 0167) the id says everything the row said.
--
-- # NULL is UNKNOWN, and it is not an error
--
-- Every line written before this migration has no origin, and so does a line a
-- recovered saga places from a plan written before the checkout carried one.
-- The money was taken either way; refusing the order for a missing origin would
-- trade a sold order for a tidy column. The CHECK below holds the three
-- together: all absent, a base price (id only), or a list price with its type.
--
-- Every disjunct tests IS NOT NULL before it compares. A CHECK passes when its
-- expression is NULL, not only when it is TRUE, and btrim(NULL) <> '' is NULL:
-- without the guards, a list id with no price id, or a type with no list,
-- passed the constraint as UNKNOWN.
--
-- # No foreign key
--
-- The ids are pricing's, and a price row is deleted when its set is replaced
-- (ADR 0047). The line names what was true when it was sold; pricing's history
-- is where that row is still readable.
ALTER TABLE order_line_items
    ADD COLUMN IF NOT EXISTS price_id        TEXT NULL,
    ADD COLUMN IF NOT EXISTS price_list_id   TEXT NULL,
    ADD COLUMN IF NOT EXISTS price_list_type TEXT NULL;

ALTER TABLE order_line_items
    ADD CONSTRAINT order_line_items_price_origin_coherent CHECK (
        (price_id IS NULL AND price_list_id IS NULL AND price_list_type IS NULL)
        OR (price_id IS NOT NULL AND btrim(price_id) <> ''
            AND price_list_id IS NULL AND price_list_type IS NULL)
        OR (price_id IS NOT NULL AND btrim(price_id) <> ''
            AND price_list_id IS NOT NULL AND btrim(price_list_id) <> ''
            AND price_list_type IS NOT NULL AND price_list_type IN ('sale', 'override'))
    );
