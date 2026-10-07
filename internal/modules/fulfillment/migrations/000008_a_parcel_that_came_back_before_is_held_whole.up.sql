-- A parcel that came back before ADR 0423 stays held whole.
--
-- ADR 0423 holds a parcel that came back undelivered ('returned') only as far as
-- a return or a replacement speaks for its units, and the order owes the rest
-- again. Before it, such a parcel held every unit, and an operator may have
-- settled those units in a way the rule cannot see: a claim refunded in money,
-- or the stock adjusted by hand. Read under the new rule, those parcels would
-- offer their units to a second parcel or put them on the shelf a second time.
--
-- So every parcel already 'returned' when this migration runs is marked held
-- whole, and the held count reads it as it did before (ADR 0423). A parcel that
-- comes back after it carries the default and is read under the new rule.
--
-- # Three edges
--
-- During a rolling deploy, an instance still on the old code that marks a
-- parcel come back after this migration committed leaves it held_whole false and
-- publishes no fulfillment.returned: the parcel is read under the new rule, and
-- a line written off while it was on the way is not recounted. Upgrade with the
-- come-back report idle, or accept that.
--
-- The backfill is not repeat-safe: rolled back and applied again, it marks held
-- whole every parcel that came back under the new rule as well (the down file
-- says so).
--
-- A parcel held whole is never offered again. An operator puts its units back
-- on the shelf by recording an order return for them and receiving it, the path
-- the count assumed before ADR 0423.
ALTER TABLE fulfillments
    ADD COLUMN IF NOT EXISTS held_whole BOOLEAN NOT NULL DEFAULT false;

UPDATE fulfillments SET held_whole = true WHERE status = 'returned';
