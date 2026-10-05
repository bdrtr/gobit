-- A refund is found by its cause (ADR 0406).
--
-- The order module documents a return's or a claim's refund on the invoice
-- that amends its sale, and reads the refunds of one order's returns and
-- claims by their ids rather than by a window: an act is documented whenever
-- someone asks, long after the quarter a journal read covers. The reference is
-- the cause's id (ADR 0187), and most refunds an operator made carry none, so
-- the index leaves those out.
CREATE INDEX IF NOT EXISTS refunds_reference_idx ON refunds (reference) WHERE reference <> '';
