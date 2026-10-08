-- Rolling 000009 back drops the item's order, with its CHECK and its index.
--
-- The code before ADR 0428 counts a parcel's items for the order the parcel was
-- opened for, so an addition's units that joined a parcel after the upgrade are
-- counted for no order again, as before the record (D264).
--
-- It is not undone by applying 000009 again. The up's backfill gives every
-- item its parcel's reference, so a joined addition's items become the
-- parent's, and asking that join again finds none of the addition's in the
-- parcel, owes the units again and fails on fulfillment_items_line_uniq. Do
-- not roll 000009 back and forth on a live database once a join has written
-- items.
ALTER TABLE fulfillment_items
    DROP COLUMN IF EXISTS reference;
