-- Putting back 000002's expression index before dropping the column it replaced,
-- so that a database rolled back to 000002 has the index that migration's
-- erasure count depends on. The order matters only in that the index must exist
-- again before anything queries it; it is recreated first so a failure between
-- the two statements leaves the older, working shape rather than neither.
CREATE INDEX IF NOT EXISTS invoices_buyer_email_idx ON invoices (lower(buyer_email));

DROP INDEX IF EXISTS invoices_buyer_email_folded_idx;

ALTER TABLE invoices
    DROP COLUMN IF EXISTS buyer_email_folded;
