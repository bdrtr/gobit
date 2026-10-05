-- An order line keeps what one unit of its goods cost the shop (ADR 0401): the
-- variant's cost in the order's currency, net of tax, copied by the checkout at
-- the moment of sale as the title and the price are, so a cost changed later
-- changes no order. NULL is UNKNOWN: a line written before this column, a line
-- sold in a currency its variant had no cost in, and a line a recovered saga
-- placed from a plan that carried none. Zero is a cost. The bound is a unit
-- price's.
ALTER TABLE order_line_items
    ADD COLUMN IF NOT EXISTS unit_cost BIGINT;

ALTER TABLE order_line_items
    ADD CONSTRAINT order_line_items_unit_cost_range
        CHECK (unit_cost IS NULL OR (unit_cost >= 0 AND unit_cost <= 1000000000000));
