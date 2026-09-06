-- Rolling 000004 back: the status vocabulary and the stamp constraints return
-- to what 000001 left, and returned_at goes.
--
-- # This rollback CAN FAIL, and that is the decision rather than a defect
--
-- If any row holds status = 'returned' the narrowed CHECK below is refused by
-- PostgreSQL and the down stops on that statement. Nothing else here is
-- conditional; this is the one statement a real table can trip.
--
-- The alternative was to rewrite those rows first — UPDATE ... SET status =
-- 'canceled' — so the rollback always succeeds. It was refused. This table is
-- the record used to reconcile against the carrier, external_id is the field
-- that matches the two systems up, and 'canceled' means "we recalled this
-- parcel". Writing it over a parcel that the carrier could not deliver and
-- returned would put a false statement about the physical world into the exact
-- column somebody reads to find out where the parcel is, and it would do so
-- silently, during a deploy rollback nobody is watching closely.
--
-- A rollback that stops is recoverable: the operator can see the rows, decide
-- what each one becomes, write it themselves and run the down again. A rollback
-- that rewrote them is not — the original status is gone and there is nothing
-- left saying it was ever different.
--
-- With no returned rows — which is every installation that has not yet met an
-- undeliverable parcel, and every test — the down is unconditional and the
-- module stays rollable.
--
-- # The stamp constraint goes FIRST
--
-- fulfillments_returned_stamp is a FULL MIRROR: it forbids returned_at without
-- the status as well as the status without the stamp. Dropping the column while
-- it stood would leave a constraint naming a column that no longer exists;
-- dropping it before the status CHECK is narrowed also stops the two from
-- disagreeing for the duration of a statement. This is the same ordering, and
-- the same reason, as the order module's 000008 down.
ALTER TABLE fulfillments DROP CONSTRAINT IF EXISTS fulfillments_returned_stamp;

ALTER TABLE fulfillments DROP CONSTRAINT IF EXISTS fulfillments_status_valid;
ALTER TABLE fulfillments ADD CONSTRAINT fulfillments_status_valid
    CHECK (status IN ('pending', 'shipped', 'delivered', 'canceled'));

-- No index names returned_at in a predicate, so nothing is silently dropped
-- with the column here. That is worth stating rather than assuming: 000003 had
-- to rebuild three indexes for exactly this reason, and the check was made
-- again for this column rather than inherited.
ALTER TABLE fulfillments DROP COLUMN IF EXISTS returned_at;
