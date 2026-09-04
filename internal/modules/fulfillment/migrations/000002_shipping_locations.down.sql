-- Rollback of 000002_shipping_locations.
--
-- The tables are dropped in the REVERSE of dependency order: first the one that
-- references (shipping_location_regions), then the referenced one
-- (shipping_locations). Indexes drop together with the table and are not
-- DROPped separately.
--
-- The rollback deletes the policy's DATA as well and that is unavoidable: the
-- data sits only in these two tables. Returning the schema to its state in
-- 000001 also returns selection to that day's rule ("the candidate with the
-- smallest id") — the fallback on the code side already follows the same path,
-- because a warehouse without a policy row is counted as being at the default
-- priority and serving all regions.

DROP TABLE IF EXISTS shipping_location_regions;
DROP TABLE IF EXISTS shipping_locations;
