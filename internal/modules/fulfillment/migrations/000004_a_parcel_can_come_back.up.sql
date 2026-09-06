-- fulfillments gains the 'returned' status and the moment that dates it.
--
-- # What was missing
--
-- Every Turkish carrier (Yurtici, Aras, MNG, PTT) reports a terminal event
-- meaning "iade": the parcel could not be delivered — the recipient was not
-- found, refused it, or the address was wrong — and it CAME BACK to the sender
-- under the original waybill. Until this migration the schema accepted four
-- statuses and none of them could hold that fact.
--
-- A plugin mapping such an event had exactly three places to put it and all
-- three write something untrue:
--
--   'canceled'  asserts the shipment did not happen. A label was printed, the
--               carrier collected the parcel and it traveled in both
--               directions. It also collides with the module's own meaning of
--               the status, which is the SAGA COMPENSATION — a cancellation we
--               requested — so a returned parcel would be indistinguishable
--               from one we recalled.
--   'delivered' asserts the recipient has it, which is precisely what did not
--               happen, and the status is irreversible.
--   nothing     leaves the row reading 'shipped' forever: a parcel sitting in
--               the merchant's own warehouse, described as in transit, with no
--               event able to move it.
--
-- # This is NOT the return the module already had
--
-- The module's answer to a customer sending goods BACK after receiving them
-- stands untouched: a second fulfillment is opened on a shipping option marked
-- is_return (shipping_options.is_return, added in 000001). That is a second
-- waybill, bought deliberately, traveling the other way.
--
-- The status added here is about the FIRST waybill ending badly. The
-- distinction is physical, not a matter of naming: one case has two shipments
-- and the other has one. Collapsing them would either invent a shipment nobody
-- bought or leave the real one stuck.
--
-- # The CHECK is a FULL MIRROR, and the other three could not have been
--
-- The three stamp constraints in 000001 are one-directional
-- (status <> 'shipped' OR shipped_at IS NOT NULL): they refuse a status with no
-- moment, and say nothing about a moment with no status. That asymmetry is not
-- an oversight there, it is forced — shipped_at SURVIVES into 'delivered', so
-- the reverse direction ((shipped_at IS NOT NULL) implies status = 'shipped')
-- is false for every delivered shipment in the table.
--
-- 'returned' is different because it is TERMINAL: nothing follows it in the
-- transition table, so returned_at can never outlive its own status. The full
-- mirror is therefore expressible here and it is written, for the reason the
-- order module recorded when it added the same shape to order_exchanges
-- (docs/gaps.md D4): a one-directional check leaves a row stamped as returned
-- while reading 'shipped' perfectly legal, and that row is invisible to every
-- status filter while carrying the evidence that it should not be.
--
-- It can be added WITHOUT a data migration only because the column is new: no
-- existing row holds returned_at, and no existing row holds the status, so both
-- directions are already satisfied for the whole table. The same constraint
-- could not have been retrofitted onto a column that had been in use.
--
-- # The column has a writer in the same change
--
-- UpdateFulfillmentStatus (queries/fulfillments.sql) sets returned_at, and
-- Service.MarkReturned is bound to POST /admin/v1/fulfillments/{id}/returned.
-- A column with no writer would pass this file's own review and fail
-- TestEveryColumnIsWrittenBySomething, which now replays migrations and sees an
-- ALTER TABLE column as well.
ALTER TABLE fulfillments ADD COLUMN IF NOT EXISTS returned_at TIMESTAMPTZ;

-- The status CHECK has to be replaced rather than added to: a second CHECK
-- naming the same column would be ANDed with the first, so 'returned' would
-- still be refused by the original one and the new constraint would look like
-- it worked while changing nothing.
ALTER TABLE fulfillments DROP CONSTRAINT IF EXISTS fulfillments_status_valid;
ALTER TABLE fulfillments ADD CONSTRAINT fulfillments_status_valid
    CHECK (status IN ('pending', 'shipped', 'delivered', 'canceled', 'returned'));

ALTER TABLE fulfillments DROP CONSTRAINT IF EXISTS fulfillments_returned_stamp;
ALTER TABLE fulfillments ADD CONSTRAINT fulfillments_returned_stamp
    CHECK ((status = 'returned') = (returned_at IS NOT NULL));

-- fulfillment_manual_shipments is DELIBERATELY not touched.
--
-- That table is the ledger of the IMITATED external system, not this module's
-- domain data, and the manual provider has no way to report a return: the
-- provider contract in core/provider knows four statuses and 'returned' is not
-- among them (core/provider.FulfillmentStatus). Widening the imitation's CHECK
-- would produce a value the provider cannot write and the service's
-- providerStatus would refuse to read — a state reachable from nowhere, which
-- is the dead-code-that-looks-alive shape this repository has removed twice
-- (docs/gaps.md D4).
--
-- The carrier that CAN report it reaches the module through the inbound
-- callback ring (ADR 0028), not through the provider's Quote/Create/Cancel
-- contract, and that path writes fulfillments directly.
