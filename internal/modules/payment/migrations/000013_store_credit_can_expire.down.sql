-- Rolling back forgets when credit expires, and it STOPS while an issue names a
-- moment or an expire row exists: without the column that credit would be
-- spendable for ever, and an expire row is a kind the older vocabulary refuses.
ALTER TABLE payment_store_credit_entries
    ADD CONSTRAINT payment_store_credit_entries_none_expiring_on_rollback
        CHECK (expires_at IS NULL AND kind <> 'expire');

DROP INDEX IF EXISTS payment_store_credit_entries_expiring_idx;

ALTER TABLE payment_store_credit_entries
    DROP CONSTRAINT IF EXISTS payment_store_credit_entries_none_expiring_on_rollback,
    DROP CONSTRAINT IF EXISTS payment_store_credit_entries_expiry_on_issue,
    DROP COLUMN IF EXISTS expires_at;

ALTER TABLE payment_store_credit_entries
    DROP CONSTRAINT payment_store_credit_entries_sign_matches_kind;
ALTER TABLE payment_store_credit_entries
    ADD CONSTRAINT payment_store_credit_entries_sign_matches_kind
        CHECK ((kind = 'hold' AND amount < 0) OR (kind <> 'hold' AND amount > 0));

ALTER TABLE payment_store_credit_entries
    DROP CONSTRAINT payment_store_credit_entries_kind_valid;
ALTER TABLE payment_store_credit_entries
    ADD CONSTRAINT payment_store_credit_entries_kind_valid
        CHECK (kind IN ('issue', 'hold', 'release', 'refund'));
