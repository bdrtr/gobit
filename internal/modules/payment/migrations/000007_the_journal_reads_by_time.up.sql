-- The journal reads by time (ADR 0186).
--
-- The payment journal derives its entries, when it is asked, from four kinds of
-- row the module already keeps: captures, refunds, store credit grants and
-- loyalty grants. It asks for the ones inside a window, and none of those tables
-- had an index on the moment it records, so every export would scan every
-- payment the installation ever took.
--
-- The two ledger indexes are PARTIAL on the kinds the journal reads. A hold, a
-- release and a tender's refund are the tender's own mechanics — the capture and
-- the refund rows carry the money — so they are never an answer, and on a busy
-- ledger they are most of its rows.
CREATE INDEX IF NOT EXISTS payments_captured_at_idx
    ON payments (captured_at, id);

CREATE INDEX IF NOT EXISTS refunds_created_at_idx
    ON refunds (created_at, id);

CREATE INDEX IF NOT EXISTS payment_store_credit_entries_issued_at_idx
    ON payment_store_credit_entries (created_at, id)
    WHERE kind = 'issue';

CREATE INDEX IF NOT EXISTS payment_loyalty_entries_granted_at_idx
    ON payment_loyalty_entries (created_at, id)
    WHERE kind IN ('earn', 'reverse');
