-- Rolling back forgets which cards were closed, and it STOPS while one was.
--
-- The CHECK below is validated against the rows that exist, so on a database
-- holding a closed card the rollback fails here and changes nothing: without
-- the column the card would open again with nothing left on it, and without the
-- void row its balance would come back.
ALTER TABLE payment_gift_cards
    ADD CONSTRAINT payment_gift_cards_none_closed_on_rollback CHECK (disabled_at IS NULL);

DROP INDEX IF EXISTS payment_gift_card_entries_voided_at_idx;
DROP INDEX IF EXISTS payment_gift_card_entries_one_void;

ALTER TABLE payment_gift_card_entries
    DROP CONSTRAINT IF EXISTS payment_gift_card_entries_kind_valid,
    DROP CONSTRAINT IF EXISTS payment_gift_card_entries_sign_matches_kind,
    DROP CONSTRAINT IF EXISTS payment_gift_card_entries_session_named;

ALTER TABLE payment_gift_card_entries
    ADD CONSTRAINT payment_gift_card_entries_kind_valid
        CHECK (kind IN ('issue', 'hold', 'release', 'refund')),
    ADD CONSTRAINT payment_gift_card_entries_sign_matches_kind
        CHECK ((kind = 'hold' AND amount < 0) OR (kind <> 'hold' AND amount > 0)),
    ADD CONSTRAINT payment_gift_card_entries_session_named
        CHECK ((kind = 'issue') = (reference = ''));

ALTER TABLE payment_gift_cards
    DROP CONSTRAINT IF EXISTS payment_gift_cards_none_closed_on_rollback,
    DROP CONSTRAINT IF EXISTS payment_gift_cards_disable_reason_not_blank,
    DROP CONSTRAINT IF EXISTS payment_gift_cards_disable_explained,
    DROP COLUMN IF EXISTS disable_reason,
    DROP COLUMN IF EXISTS disabled_at;
