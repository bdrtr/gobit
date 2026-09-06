-- fulfillments loses its deleted_at column.
--
-- # Why the column goes rather than gaining a writer
--
-- A fulfillment is THE RECORD OF SOMETHING THAT HAPPENED: a shipment was handed
-- to a carrier, a label may already be printed and the parcel may already be on
-- a van. Its retirement is a STATUS — 'canceled' with canceled_at, and the
-- fulfillments_canceled_stamp CHECK refuses the status without the moment — and
-- the module already had that answer written down. The head of 000001 lists the
-- exceptions to soft delete and explains fulfillment_items with exactly this
-- argument: "when the shipment is canceled the line is not deleted, the
-- shipment's status changes". That sentence is about this table and it was
-- never applied to it.
--
-- The column was never written. Every read since the module was created has
-- carried "deleted_at IS NULL", a predicate that has never once been false, and
-- the audit built to catch that could not see it until 2026-09-06: three
-- SIBLING tables here (shipping_profiles, shipping_options,
-- shipping_option_rules) really are soft-deleted, and the audit matched writes
-- by bare column name across the module (docs/gaps.md D16, D18).
--
-- The rejected alternative was to write it — to give the module a
-- DeleteFulfillment. It was refused because the state it would produce has no
-- meaning here: a canceled shipment must stay visible for reconciliation
-- against the provider (external_id is what matches the two systems up), and a
-- shipment hidden from every read while the carrier still holds the parcel is a
-- record the operator needs precisely when they cannot find it.
--
-- The order and payment modules hold ten columns of the same shape recorded as
-- a DECISION rather than removed (docs/gaps.md D9). The ARGUMENT here is the
-- same as theirs — a record of something that happened is kept, and the retreat
-- from it is a row or a status, not a deletion. The CONCLUSION differs, and the
-- reason is the index below: this table's uniqueness rule is written as
-- "unique among LIVING rows", so as long as the column stands there is a way to
-- write a second live shipment against the same idempotency key by stamping the
-- first. D9's ten carry no such rule.
--
-- # The indexes have to be rebuilt, and one of them is load-bearing
--
-- PostgreSQL drops any index whose PREDICATE names a dropped column, silently
-- and with no notice. All three indexes on this table are partial on
-- deleted_at, and one of them — fulfillments_idempotency_uniq — is the SINGLE
-- POINT of the race that stops a retried saga step from producing A SECOND
-- SHIPPING LABEL. Measured on a real PostgreSQL 16 before this migration was
-- written: on a probe table with a UNIQUE partial index, DROP COLUMN removed
-- the index and the duplicate key that had been impossible one statement
-- earlier was accepted. Dropping the column without the statements below would
-- have removed the guard and left the schema looking untouched.
ALTER TABLE fulfillments DROP COLUMN IF EXISTS deleted_at;

-- The idempotency key is now unique among ALL shipments rather than among
-- living ones, which is what the module always meant: there is no way to stop
-- being a shipment.
CREATE UNIQUE INDEX IF NOT EXISTS fulfillments_idempotency_uniq
    ON fulfillments (idempotency_key);

CREATE INDEX IF NOT EXISTS fulfillments_reference_idx
    ON fulfillments (reference, created_at DESC);

-- Renamed from fulfillments_alive_idx. The old name described the PREDICATE
-- that no longer exists; a name that says "alive" on an index over every row
-- would be the next reader's wrong assumption.
DROP INDEX IF EXISTS fulfillments_alive_idx;
CREATE INDEX IF NOT EXISTS fulfillments_listing_idx
    ON fulfillments (created_at DESC, id DESC);
