-- order_replacement_item_parts: what one unit of a replacement item holds when
-- the item replaces a line that sold a bundle, and the promise each part's units
-- are held under (ADR 0238).
--
-- The rows are copied from the line's components when the item is written, so
-- a replacement sends the parts the sale took, whatever the bundle is made of
-- by the time it leaves. The dispatch sets each part's units aside and writes
-- its promise here, as it writes a plain item's on the item's own column; an
-- item with parts takes no promise of its own.
CREATE TABLE IF NOT EXISTS order_replacement_item_parts (
    order_replacement_item_id TEXT        NOT NULL REFERENCES order_replacement_items (id) ON DELETE CASCADE,
    variant_id                TEXT        NOT NULL,
    quantity                  BIGINT      NOT NULL,
    rank                      INTEGER     NOT NULL,
    reservation_id            TEXT,
    created_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (order_replacement_item_id, variant_id),
    CONSTRAINT order_replacement_item_parts_rank_unique UNIQUE (order_replacement_item_id, rank),
    CONSTRAINT order_replacement_item_parts_quantity CHECK (quantity BETWEEN 1 AND 100),
    CONSTRAINT order_replacement_item_parts_variant_not_blank CHECK (variant_id <> ''),
    CONSTRAINT order_replacement_item_parts_reservation_not_blank CHECK (reservation_id IS NULL OR reservation_id <> '')
);
