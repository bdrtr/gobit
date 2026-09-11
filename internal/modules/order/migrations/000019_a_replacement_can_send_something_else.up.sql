-- A replacement can send a DIFFERENT product, not only more of the same one.
--
-- # What could not be promised
--
-- Every after-sales item in this module points at an existing order line:
-- `order_return_items.order_line_item_id`, `order_replacement_items.order_line_item_id`,
-- `order_line_cancellations.order_line_item_id`. That is right for the ones that
-- talk about goods already sold — a return is of a thing the customer received.
--
-- It is wrong for a replacement. "Send me the same shirt in a larger size" is the
-- ordinary exchange, and the only thing this table could express was units of the
-- EXACT variant already on the order. An exchange's money half has been able to
-- take a difference since ADR 0120 (`order_exchanges.difference_due`, funded
-- through its own collection); there was nothing for that money to answer.
--
-- # Why a column and not a table
--
-- Because the two shapes are the same fact — "this many of this thing is going
-- out" — and only the way the thing is named differs. A second table would give
-- the dispatch flow two lists to merge and two places for the ceiling rule to
-- drift apart.
ALTER TABLE order_replacement_items
    ADD COLUMN IF NOT EXISTS variant_id TEXT;

-- The line becomes optional, because a variant-shaped row has none to point at.
ALTER TABLE order_replacement_items
    ALTER COLUMN order_line_item_id DROP NOT NULL;

-- EXACTLY ONE of the two names the goods.
--
-- Neither would be a row promising nothing. Both would be two answers to "what is
-- being sent", and the dispatch would have to pick — silently, and differently
-- from whatever a later reader assumed.
ALTER TABLE order_replacement_items
    ADD CONSTRAINT order_replacement_items_names_one_thing
    CHECK ((order_line_item_id IS NULL) <> (variant_id IS NULL));

ALTER TABLE order_replacement_items
    ADD CONSTRAINT order_replacement_items_variant_not_blank
    CHECK (variant_id IS NULL OR length(btrim(variant_id)) > 0);

-- The uniqueness becomes PARTIAL, and gains a sibling.
--
-- One row per (replacement, line) was the rule and it still is — for the rows
-- that name a line. A NULL is not equal to another NULL in a unique index, so
-- leaving the old index alone would have let a replacement carry the same variant
-- twice; the sibling says the same thing on the other side.
DROP INDEX IF EXISTS order_replacement_items_line_uniq;

CREATE UNIQUE INDEX IF NOT EXISTS order_replacement_items_line_uniq
    ON order_replacement_items (order_replacement_id, order_line_item_id)
    WHERE order_line_item_id IS NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS order_replacement_items_variant_uniq
    ON order_replacement_items (order_replacement_id, variant_id)
    WHERE variant_id IS NOT NULL;
