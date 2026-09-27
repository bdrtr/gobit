-- A gift card can expire (ADR 0214).
--
-- The moment is set when the card is made and never moves: an operator may
-- name it, and otherwise the installation's validity decides it, from the same
-- now() the card's created_at is stamped with. A card made before this column,
-- or while the validity is zero, has none and never expires.
ALTER TABLE payment_gift_cards
    ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ;

ALTER TABLE payment_gift_cards
    ADD CONSTRAINT payment_gift_cards_expires_after_issue
        CHECK (expires_at IS NULL OR expires_at > created_at);

-- The expiry job reads the open cards whose moment has come, oldest first.
CREATE INDEX IF NOT EXISTS payment_gift_cards_expiring_idx
    ON payment_gift_cards (expires_at, id)
    WHERE disabled_at IS NULL AND expires_at IS NOT NULL;
