-- Store credit can expire (ADR 0258).
--
-- An issue may name the moment its credit expires; no other kind carries one,
-- because a hold, a release or a refund is money moving, not a grant. What the
-- expired credit still holds is taken back by an expire row, which is negative
-- like a hold and, like every row here, never edited.
ALTER TABLE payment_store_credit_entries
    ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ;

ALTER TABLE payment_store_credit_entries
    ADD CONSTRAINT payment_store_credit_entries_expiry_on_issue
        CHECK (expires_at IS NULL OR (kind = 'issue' AND expires_at > created_at));

ALTER TABLE payment_store_credit_entries
    DROP CONSTRAINT payment_store_credit_entries_kind_valid;
ALTER TABLE payment_store_credit_entries
    ADD CONSTRAINT payment_store_credit_entries_kind_valid
        CHECK (kind IN ('issue', 'hold', 'release', 'refund', 'expire'));

ALTER TABLE payment_store_credit_entries
    DROP CONSTRAINT payment_store_credit_entries_sign_matches_kind;
ALTER TABLE payment_store_credit_entries
    ADD CONSTRAINT payment_store_credit_entries_sign_matches_kind
        CHECK ((kind IN ('hold', 'expire') AND amount < 0)
            OR (kind NOT IN ('hold', 'expire') AND amount > 0));

-- The expiry job finds the balances holding an issue whose moment has come.
CREATE INDEX IF NOT EXISTS payment_store_credit_entries_expiring_idx
    ON payment_store_credit_entries (expires_at)
    WHERE kind = 'issue' AND expires_at IS NOT NULL;
