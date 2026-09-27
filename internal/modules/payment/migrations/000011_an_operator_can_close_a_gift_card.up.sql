-- An operator can close a gift card (ADR 0213).
--
-- A closed card is refused as a payment, and what it still held is written off
-- by a void row in the same transaction: the balance of a closed card is zero,
-- and the journal books the void (a granted card's cost comes back, a sold
-- card's price is kept). The reason is required, as an issue's is: a card that
-- stopped working for no recorded reason is a question nobody can answer later.
ALTER TABLE payment_gift_cards
    ADD COLUMN IF NOT EXISTS disabled_at    TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS disable_reason TEXT;

ALTER TABLE payment_gift_cards
    ADD CONSTRAINT payment_gift_cards_disable_explained
        CHECK ((disabled_at IS NULL) = (disable_reason IS NULL)),
    ADD CONSTRAINT payment_gift_cards_disable_reason_not_blank
        CHECK (disable_reason IS NULL OR length(btrim(disable_reason)) > 0);

-- The void joins the vocabulary. It takes the balance away, so it is negative
-- as a hold is, and like the issue it belongs to no payment session.
ALTER TABLE payment_gift_card_entries
    DROP CONSTRAINT IF EXISTS payment_gift_card_entries_kind_valid,
    DROP CONSTRAINT IF EXISTS payment_gift_card_entries_sign_matches_kind,
    DROP CONSTRAINT IF EXISTS payment_gift_card_entries_session_named;

ALTER TABLE payment_gift_card_entries
    ADD CONSTRAINT payment_gift_card_entries_kind_valid
        CHECK (kind IN ('issue', 'hold', 'release', 'refund', 'void')),
    ADD CONSTRAINT payment_gift_card_entries_sign_matches_kind
        CHECK ((kind IN ('hold', 'void') AND amount < 0) OR (kind NOT IN ('hold', 'void') AND amount > 0)),
    ADD CONSTRAINT payment_gift_card_entries_session_named
        CHECK ((kind IN ('issue', 'void')) = (reference = ''));

-- A card is closed once, so it is voided once.
CREATE UNIQUE INDEX IF NOT EXISTS payment_gift_card_entries_one_void
    ON payment_gift_card_entries (gift_card_id) WHERE kind = 'void';

-- The journal reads the voids by time, as it reads the issues.
CREATE INDEX IF NOT EXISTS payment_gift_card_entries_voided_at_idx
    ON payment_gift_card_entries (created_at, id)
    WHERE kind = 'void';
