-- A line the checkout let through without stock waits for its units (ADR 0392).
--
-- Until this table, a backorder-permitting line no warehouse could cover was named
-- only in the checkout's execution record (ADR 0048): the units that arrived next
-- went to the next shopper, and a write-off of the line was credited to whichever
-- shelf another line of the order had sold the same item from (D242).
--
-- A row is a CLAIM: the order line is owed quantity units of the item, fillable at
-- the warehouses in location_ids. Every write that raises a level's sellable
-- quantity fills the oldest waiting claims there it can complete, whole, as a
-- reservation of the order confirmed in that write's transaction.
--
-- order_id and order_line_item_id belong to the order module and are NOT foreign
-- keys (Principle 2.2). location_ids are the warehouses fulfillment ranked for the
-- order when it was placed, in that order; empty means none could be ranked.
CREATE TABLE IF NOT EXISTS inventory_backorders (
    id                 TEXT        PRIMARY KEY,
    inventory_item_id  TEXT        NOT NULL REFERENCES inventory_items (id),
    order_id           TEXT        NOT NULL,
    order_line_item_id TEXT        NOT NULL,
    quantity           BIGINT      NOT NULL,
    withdrawn_quantity BIGINT      NOT NULL DEFAULT 0,
    location_ids       TEXT[]      NOT NULL,
    status             TEXT        NOT NULL DEFAULT 'waiting',
    reservation_id     TEXT        REFERENCES inventory_reservations (id),
    -- Where the fill deducted: the shelf a write-off of this line goes back to.
    filled_location_id TEXT        REFERENCES stock_locations (id),
    -- The queue position (ADR 0233's seq).
    seq                BIGINT      GENERATED ALWAYS AS IDENTITY,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT inventory_backorders_quantity_positive CHECK (quantity > 0),
    CONSTRAINT inventory_backorders_withdrawn_bounded
        CHECK (withdrawn_quantity >= 0 AND withdrawn_quantity <= quantity),
    CONSTRAINT inventory_backorders_status_valid
        CHECK (status IN ('waiting', 'filled', 'withdrawn')),
    CONSTRAINT inventory_backorders_filled_names_its_reservation
        CHECK ((status = 'filled') = (reservation_id IS NOT NULL)),
    CONSTRAINT inventory_backorders_filled_names_its_location
        CHECK ((reservation_id IS NULL) = (filled_location_id IS NULL)),
    CONSTRAINT inventory_backorders_withdrawn_is_whole
        CHECK ((status = 'withdrawn') = (withdrawn_quantity = quantity)),
    CONSTRAINT inventory_backorders_ids_not_blank
        CHECK (length(btrim(order_id)) > 0 AND length(btrim(order_line_item_id)) > 0),
    CONSTRAINT inventory_backorders_locations_not_null
        CHECK (array_position(location_ids, NULL) IS NULL)
);

-- One claim per line and item: a checkout recovery that runs the last step again
-- finds the claim it wrote rather than writing a second.
CREATE UNIQUE INDEX IF NOT EXISTS inventory_backorders_line_uniq
    ON inventory_backorders (order_line_item_id, inventory_item_id);

-- The queue a fill reads under the level's lock: the item's waiting claims in
-- seq order.
CREATE INDEX IF NOT EXISTS inventory_backorders_queue_idx
    ON inventory_backorders (inventory_item_id, seq) WHERE status = 'waiting';

-- The operator's listing, every status.
CREATE INDEX IF NOT EXISTS inventory_backorders_item_idx
    ON inventory_backorders (inventory_item_id, seq);

-- A sale's reservation names at most one claim, and the ledger's way back asks
-- whether it names one (SaleLocationsForReference).
CREATE UNIQUE INDEX IF NOT EXISTS inventory_backorders_reservation_uniq
    ON inventory_backorders (reservation_id) WHERE reservation_id IS NOT NULL;
