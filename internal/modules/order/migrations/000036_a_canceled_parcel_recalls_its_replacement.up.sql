-- A canceled parcel recalls its replacement (ADR 0239).
--
-- recalls counts the parcels canceled under the replacement. Each one sent the
-- record back to 'requested', and the next parcel is opened under a key that
-- names the count, because a key that resolves to a canceled parcel is refused
-- (ADR 0088).
ALTER TABLE order_replacements
    ADD COLUMN IF NOT EXISTS recalls INTEGER NOT NULL DEFAULT 0;
ALTER TABLE order_replacements
    ADD CONSTRAINT order_replacements_recalls_nonneg CHECK (recalls >= 0);

-- The canceled parcel names its replacement through this column.
CREATE INDEX IF NOT EXISTS order_replacements_fulfillment_idx
    ON order_replacements (fulfillment_id)
    WHERE fulfillment_id IS NOT NULL;
