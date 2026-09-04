-- Rollback of 000001_fulfillment_init.
--
-- The tables are dropped in the REVERSE of dependency order: first the ones
-- that reference (fulfillment_items -> fulfillments -> shipping_option_rules ->
-- shipping_options), then the referenced one (shipping_profiles). Indexes drop
-- together with the table and are not DROPped separately.
--
-- fulfillment_manual_shipments is bound to no table; it is the provider's own
-- ledger and its order does not matter.

DROP TABLE IF EXISTS fulfillment_manual_shipments;
DROP TABLE IF EXISTS fulfillment_items;
DROP TABLE IF EXISTS fulfillments;
DROP TABLE IF EXISTS shipping_option_rules;
DROP TABLE IF EXISTS shipping_options;
DROP TABLE IF EXISTS shipping_profiles;
