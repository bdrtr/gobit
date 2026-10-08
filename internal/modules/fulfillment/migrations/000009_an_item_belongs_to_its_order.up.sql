-- An item names the order its units belong to (ADR 0428, D264).
--
-- A parcel's items were the lines of the order it was opened for, the one its
-- reference names, and every count of what an order's parcels hold summed a
-- parcel's items by that reference (ADR 0409, ADR 0420, ADR 0423). An addition
-- that joins its parent's pending parcel (ADR 0197) travels in a parcel opened
-- for another order, so its units are items of a parcel whose reference is not
-- its own. reference on the item says whose they are, and the counts sum by it.
--
-- It is the order module's identifier, not a foreign key (Principle 2.2), for
-- the reason fulfillments.reference is not one in 000001. Every item already
-- written belongs to the order its parcel was opened for, so the backfill
-- copies the parcel's reference and every count reads as it did before. An
-- addition joined before ADR 0428 has no item to fill: it rode itemless.
--
-- # The rolling deploy
--
-- An instance still on the old code that opens a parcel after this migration
-- commits writes an item without a reference, the NOT NULL refuses it and the
-- open rolls back. The same instance joins an addition to a parcel without
-- items, as before ADR 0428, and its offer and write-off restock still sum a
-- parcel's items by the parcel's reference, so a write-off of a joined
-- addition's line it serves puts the boxed units back on the shelf. Upgrade
-- with nobody opening parcels, joining them or writing lines off.
ALTER TABLE fulfillment_items ADD COLUMN IF NOT EXISTS reference TEXT;

UPDATE fulfillment_items i
   SET reference = f.reference
  FROM fulfillments f
 WHERE f.id = i.fulfillment_id
   AND i.reference IS NULL;

ALTER TABLE fulfillment_items ALTER COLUMN reference SET NOT NULL;

ALTER TABLE fulfillment_items ADD CONSTRAINT fulfillment_items_reference_check
    CHECK (btrim(reference) <> '');

-- The held count reads an order's items by this column under the order's
-- dispatch lock, on every open and every write-off.
CREATE INDEX IF NOT EXISTS fulfillment_items_reference_idx
    ON fulfillment_items (reference);
