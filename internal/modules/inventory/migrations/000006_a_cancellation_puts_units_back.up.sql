-- Units written off before they shipped go back on the shelf.
--
-- The checkout DEDUCTS stock: its last step confirms the reservations, which
-- consumes them. So an order that exists has already had its units taken off the
-- sellable figure, and a line canceled afterwards is a unit that will never be
-- delivered and is no longer counted as stock either. Nothing put it back —
-- neither the whole-order cancellation nor the partial one (gap D70).
--
-- A sixth reason rather than a positive 'adjustment', for the reason the ledger
-- was built with: the arithmetic is the same and the FACT is not. An operator
-- reading 'adjustment' sees a warehouse correction, and reading 'return_restock'
-- sees goods a customer sent back. Neither happened here: nobody counted
-- anything and nothing arrived.
ALTER TABLE inventory_movements
    DROP CONSTRAINT IF EXISTS inventory_movements_reason_valid;
ALTER TABLE inventory_movements
    ADD CONSTRAINT inventory_movements_reason_valid
    CHECK (reason IN ('stock_count', 'adjustment', 'sale', 'return_restock',
                      'replacement', 'cancellation'));

-- A cancellation only ever ADDS. It is stock coming back from a unit that was
-- deducted and will not leave, so a negative one would be this reason used for
-- something it does not mean — the same guard 'replacement' carries in the
-- opposite direction.
ALTER TABLE inventory_movements
    ADD CONSTRAINT inventory_movements_cancellation_is_positive
    CHECK (reason <> 'cancellation' OR delta > 0);

-- What the movement was FOR, when the reason has something to point at.
--
-- It is the order for a sale and the cancellation row for a cancellation. Two
-- things rest on it and the second is why it is not merely informative:
--
--   * a sale's reference is what lets a later cancellation find the LOCATION the
--     units were taken from. The reservation knew it, and reservations are keyed
--     to the CART's line item, which the order does not carry — so without this
--     column the way back is a chain through three modules.
--   * a cancellation's reference makes putting the units back IDEMPOTENT. The
--     event bus delivers at least once, and adding stock is deliberately not
--     idempotent (two restocks mean two physical arrivals), so the unique index
--     below is what turns a redelivery into nothing.
ALTER TABLE inventory_movements
    ADD COLUMN IF NOT EXISTS reference TEXT;

ALTER TABLE inventory_movements
    ADD CONSTRAINT inventory_movements_reference_not_blank
    CHECK (reference IS NULL OR length(btrim(reference)) > 0);

-- A cancellation MUST say which cancellation it was, because the uniqueness
-- below is the only thing standing between a redelivered event and stock that
-- grows every time the bus retries.
ALTER TABLE inventory_movements
    ADD CONSTRAINT inventory_movements_cancellation_has_reference
    CHECK (reason <> 'cancellation' OR reference IS NOT NULL);

-- One cancellation puts its units back ONCE.
--
-- Partial rather than total: a sale's reference is the order, and an order has
-- one sale movement per inventory item, so sales are legitimately many to one
-- reference.
CREATE UNIQUE INDEX IF NOT EXISTS inventory_movements_cancellation_once_idx
    ON inventory_movements (reference)
    WHERE reason = 'cancellation';

-- Finding the location a sale took units from, which is the read the way back
-- needs.
CREATE INDEX IF NOT EXISTS inventory_movements_sale_reference_idx
    ON inventory_movements (reference)
    WHERE reason = 'sale' AND reference IS NOT NULL;
