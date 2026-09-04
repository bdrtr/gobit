-- The reconciliation index.
--
-- ListSessionsForReconciliation asks one question on a schedule: which sessions
-- are still authorized here, and have been for longer than the settling window.
-- Without an index that is a sequential scan over every payment session that
-- ever existed, run hourly, forever — and it gets slower every day the
-- installation takes money.
--
-- It is PARTIAL, on the predicate the query actually carries. Sessions leave
-- the suspect set for good the moment they are captured, canceled or declined,
-- so a full index over (status, updated_at) would spend most of its size on
-- rows that can never be an answer. On a real ledger the live-authorized set is
-- a small fraction of the table, and the index is that fraction.
--
-- updated_at is the ordering column and it is the LAST one on purpose: the
-- status and deleted_at halves of the predicate live in the index's WHERE
-- clause, so what remains to be scanned is already sorted the way the query
-- reads it, and the LIMIT stops the scan instead of sorting the whole match.
CREATE INDEX IF NOT EXISTS payment_sessions_reconcile_idx
    ON payment_sessions (updated_at)
    WHERE status = 'authorized' AND deleted_at IS NULL;
