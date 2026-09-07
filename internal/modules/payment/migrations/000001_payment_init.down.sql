-- Undoing 000001_payment_init.
--
-- The tables are dropped in the REVERSE of the dependency order: the ones that
-- reference first (refunds -> payments -> payment_sessions), then the one they
-- reference (payment_collections). Indexes fall together with their table and
-- are not DROPped separately.
--
-- payment_manual_sessions depends on no table; it is the provider's own ledger
-- and its position here does not matter.

DROP TABLE IF EXISTS payment_manual_sessions;
DROP TABLE IF EXISTS refunds;
DROP TABLE IF EXISTS payments;
DROP TABLE IF EXISTS payment_sessions;
DROP TABLE IF EXISTS payment_collections;
