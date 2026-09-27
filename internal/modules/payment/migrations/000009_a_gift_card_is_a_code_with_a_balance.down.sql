-- Rolling back drops the gift cards, and it STOPS while one exists.
--
-- A card is money the shop owes whoever holds its code, and no other table
-- holds it. The CHECK below is validated against the rows that exist, so on a
-- database holding a card the rollback fails here and deletes nothing: a
-- rollback that stops is recoverable, one that dropped a debt is not (the
-- module's rule since 000003). An operator who means it deletes the cards first.
ALTER TABLE payment_gift_cards
    ADD CONSTRAINT payment_gift_cards_none_on_rollback CHECK (false);

DROP TABLE IF EXISTS payment_gift_card_sessions;

DROP TABLE IF EXISTS payment_gift_card_entries;

DROP TABLE IF EXISTS payment_gift_cards;
