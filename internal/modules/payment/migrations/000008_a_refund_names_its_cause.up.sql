-- A refund names its cause (ADR 0187).
--
-- A refund row recorded how much went back and a free-text reason, and nothing
-- the order module could read to tell a return's refund from a claim's or from
-- an operator's. The reference is the caller's id for the record that caused
-- the refund — a return, a claim, an exchange — written in the SAME transaction
-- as the refund, so the money cannot leave without it. It is empty for a refund
-- nobody attributed, and this module never reads it: it is the caller's key, as
-- payment_collections.reference is.
ALTER TABLE refunds
    ADD COLUMN IF NOT EXISTS reference TEXT NOT NULL DEFAULT '';

ALTER TABLE refunds
    ADD CONSTRAINT refunds_reference_trimmed
    CHECK (reference = btrim(reference));
