-- Rolling 000001_inventory_init back.
--
-- The tables are dropped in the REVERSE of the dependency order: first the
-- tables that reference inventory_items and stock_locations, then the ones
-- referenced. The indexes fall with their table and are not DROPped separately.

DROP TABLE IF EXISTS inventory_reservations;
DROP TABLE IF EXISTS inventory_levels;
DROP TABLE IF EXISTS inventory_items;
DROP TABLE IF EXISTS stock_locations;
