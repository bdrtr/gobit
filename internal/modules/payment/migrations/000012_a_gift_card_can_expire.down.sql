-- Rolling back forgets when cards expire, and it STOPS while a card has a moment:
-- without the column such a card would pay for ever.
ALTER TABLE payment_gift_cards
    ADD CONSTRAINT payment_gift_cards_none_expiring_on_rollback CHECK (expires_at IS NULL);

DROP INDEX IF EXISTS payment_gift_cards_expiring_idx;

ALTER TABLE payment_gift_cards
    DROP CONSTRAINT IF EXISTS payment_gift_cards_none_expiring_on_rollback,
    DROP CONSTRAINT IF EXISTS payment_gift_cards_expires_after_issue,
    DROP COLUMN IF EXISTS expires_at;
