-- Rolling back forgets which cards were sold, and it STOPS while one was.
--
-- The CHECK below is validated against the rows that exist, so on a database
-- holding a sold card the rollback fails here and changes nothing: the sale a
-- card came from is the only record of it this module keeps.
ALTER TABLE payment_gift_cards
    ADD CONSTRAINT payment_gift_cards_none_sold_on_rollback CHECK (source <> 'sold');

DROP INDEX IF EXISTS payment_gift_cards_source_reference_uniq;

ALTER TABLE payment_gift_cards
    DROP CONSTRAINT IF EXISTS payment_gift_cards_none_sold_on_rollback,
    DROP CONSTRAINT IF EXISTS payment_gift_cards_sold_names_its_sale,
    DROP CONSTRAINT IF EXISTS payment_gift_cards_source_valid,
    DROP COLUMN IF EXISTS code_changed_at,
    DROP COLUMN IF EXISTS source_reference,
    DROP COLUMN IF EXISTS source;
