-- Units a supplier owes a warehouse (ADR 0399).
--
-- Until this table the module knew what was on the shelf and nothing about what
-- was coming: a supplier's delivery was written as a stock count or an
-- adjustment, which the ledger cannot tell from a correction, and no storefront
-- could say when a variant with nothing to sell would be on sale again.
--
-- A row is an EXPECTED SUPPLIER RECEIPT: quantity units of the item are owed to
-- an open warehouse and expected to be SELLABLE there at expected_at (after
-- put-away), with its offset. Receiving one writes the counted units through the
-- ledger as 'supplier_receipt' and closes the row in the same transaction;
-- cancelling closes it with nothing written. A status leaves 'expected' once.
--
-- reference is the embedder's own document number; gobit keeps no supplier,
-- price or purchase order.
CREATE TABLE IF NOT EXISTS inventory_supplier_receipts (
    id                TEXT        PRIMARY KEY,
    inventory_item_id TEXT        NOT NULL REFERENCES inventory_items (id),
    location_id       TEXT        NOT NULL REFERENCES stock_locations (id),
    quantity          BIGINT      NOT NULL,
    expected_at       TIMESTAMPTZ NOT NULL,
    reference         TEXT,
    status            TEXT        NOT NULL DEFAULT 'expected',
    -- Both copied from the supplier_receipt movement that closed the row, so the
    -- ledger and the record cannot disagree.
    received_quantity BIGINT,
    received_at       TIMESTAMPTZ,
    canceled_at       TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT inventory_supplier_receipts_quantity_positive CHECK (quantity > 0),
    CONSTRAINT inventory_supplier_receipts_status_valid
        CHECK (status IN ('expected', 'received', 'canceled')),
    CONSTRAINT inventory_supplier_receipts_received_names_its_count
        CHECK ((status = 'received') = (received_quantity IS NOT NULL)),
    CONSTRAINT inventory_supplier_receipts_received_names_its_moment
        CHECK ((status = 'received') = (received_at IS NOT NULL)),
    CONSTRAINT inventory_supplier_receipts_received_positive
        CHECK (received_quantity IS NULL OR received_quantity > 0),
    CONSTRAINT inventory_supplier_receipts_canceled_names_its_moment
        CHECK ((status = 'canceled') = (canceled_at IS NOT NULL)),
    CONSTRAINT inventory_supplier_receipts_reference_not_blank
        CHECK (reference IS NULL OR length(btrim(reference)) > 0)
);

-- The operator's listing, every status.
CREATE INDEX IF NOT EXISTS inventory_supplier_receipts_item_idx
    ON inventory_supplier_receipts (inventory_item_id, expected_at, id);

-- The forecast and the item deletion's count read only what is still expected.
CREATE INDEX IF NOT EXISTS inventory_supplier_receipts_expected_item_idx
    ON inventory_supplier_receipts (inventory_item_id, expected_at, id)
    WHERE status = 'expected';

-- The close's count.
CREATE INDEX IF NOT EXISTS inventory_supplier_receipts_expected_location_idx
    ON inventory_supplier_receipts (location_id)
    WHERE status = 'expected';

-- A seventh reason. A delivery is neither a correction nor goods a customer sent
-- back, and the ledger exists to tell those apart (ADR 0068).
ALTER TABLE inventory_movements
    DROP CONSTRAINT IF EXISTS inventory_movements_reason_valid;
ALTER TABLE inventory_movements
    ADD CONSTRAINT inventory_movements_reason_valid
    CHECK (reason IN ('stock_count', 'adjustment', 'sale', 'return_restock',
                      'replacement', 'cancellation', 'supplier_receipt'));

-- A receipt only ever adds.
ALTER TABLE inventory_movements
    ADD CONSTRAINT inventory_movements_supplier_receipt_adds
    CHECK (reason <> 'supplier_receipt' OR delta > 0);

-- It names the receipt it closed.
ALTER TABLE inventory_movements
    ADD CONSTRAINT inventory_movements_supplier_receipt_names_its_receipt
    CHECK (reason <> 'supplier_receipt' OR reference IS NOT NULL);

-- One movement per receipt: a receipt is received once, and the row the receipt
-- copies its count and moment from is this one.
CREATE UNIQUE INDEX IF NOT EXISTS inventory_movements_supplier_receipt_once_idx
    ON inventory_movements (reference)
    WHERE reason = 'supplier_receipt';
