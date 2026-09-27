-- A gift card can be sold as well as issued (ADR 0210).
--
-- source says which: 'issued' by an operator, 'sold' on an order. A sold card
-- names the order line and unit it was sold as in source_reference, and that
-- reference is unique: the flow that issues sold cards runs on an event the bus
-- delivers at least once, and a second delivery must find the card the first
-- one made rather than make another.
--
-- code_changed_at is when the card's code was last replaced. The code is the
-- one column of a card that changes: a code that did not reach its holder is
-- replaced, and the balance stays with the card.
ALTER TABLE payment_gift_cards
    ADD COLUMN IF NOT EXISTS source           TEXT NOT NULL DEFAULT 'issued',
    ADD COLUMN IF NOT EXISTS source_reference TEXT,
    ADD COLUMN IF NOT EXISTS code_changed_at  TIMESTAMPTZ;

ALTER TABLE payment_gift_cards
    ADD CONSTRAINT payment_gift_cards_source_valid
        CHECK (source IN ('issued', 'sold')),
    ADD CONSTRAINT payment_gift_cards_sold_names_its_sale
        CHECK ((source = 'sold') = (source_reference IS NOT NULL));

CREATE UNIQUE INDEX IF NOT EXISTS payment_gift_cards_source_reference_uniq
    ON payment_gift_cards (source_reference) WHERE source_reference IS NOT NULL;
