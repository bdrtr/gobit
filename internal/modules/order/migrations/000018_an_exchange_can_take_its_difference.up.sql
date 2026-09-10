-- An exchange that owes money can take it, and taking it is a STATE.
--
-- # What 000017 left, and what changed under it
--
-- 000017 brought the completion back for the case where both halves were
-- answerable: the goods left and there was no money to move. It wrote down why
-- the other half was missing -- "the order-to-payment link is still one-to-one,
-- so money cannot be collected against an existing order" -- and bounded the
-- completion with a CHECK on `difference_due = 0`.
--
-- That reason is no longer the one. ADR 0117 decided the sale's link STAYS one
-- to one and that other money gets its own binding; ADR 0118 made a collection's
-- gate read the capacity left rather than a flag; ADR 0119 decided that an order
-- row does not mirror an amount the payment module owns.
--
-- # Why an identifier and not an amount
--
-- ADR 0119 forbids the AMOUNT here: a figure the payment module owns can be
-- changed by a route this module never hears about, so a copy of it is a claim
-- that goes stale in silence. An IDENTIFIER cannot go stale -- a collection is
-- the collection it is -- and it is what lets a flow ask the live question.
--
-- The shape is 000012's, stated there in the same words: `fulfillment_id` is
-- the parcel and lives in the fulfillment module, `reservation_id` is the
-- promise and lives in inventory, "neither carries a foreign key (Principle
-- 2.2), and both are written by the flow that holds both sides". This column is
-- the third of that kind.
--
-- # Why 'funded' is a status and not only a stamp
--
-- The moment an exchange takes the customer's money it stops being a request
-- that can be withdrawn, and the vocabulary has to say so or the transition
-- table cannot. A status makes the refusal the table's own sentence -- the shape
-- the return already uses, where a received return refuses a cancel -- instead
-- of a rule bolted beside it.
--
-- It also keeps the bound the CHECK below can hold. A binding recorded through
-- the link layer would be invisible to a constraint; a column is not.

ALTER TABLE order_exchanges
    ADD COLUMN IF NOT EXISTS payment_collection_id TEXT,
    ADD COLUMN IF NOT EXISTS funded_at             TIMESTAMPTZ;

-- The vocabulary and the schema say the same thing (000008's rule).
ALTER TABLE order_exchanges
    DROP CONSTRAINT IF EXISTS order_exchanges_status_valid;

ALTER TABLE order_exchanges
    ADD CONSTRAINT order_exchanges_status_valid
        CHECK (status IN ('requested', 'funded', 'completed', 'canceled'));

-- A funding is a moment AND a collection, in both directions. A moment without
-- the collection is a moment nobody can check; a collection without the moment
-- is money nothing dates.
ALTER TABLE order_exchanges
    ADD CONSTRAINT order_exchanges_funded_stamp
        CHECK ((funded_at IS NOT NULL) = (payment_collection_id IS NOT NULL));

-- Only a POSITIVE difference is funded. The sign states the direction
-- (000001), and money owed TO the customer is a refund -- a different verb,
-- which this record does not have. Written as `> 0` rather than `<> 0` on
-- purpose: `<> 0` would let the negative side in and quietly undo what
-- 000017's bound protects.
ALTER TABLE order_exchanges
    ADD CONSTRAINT order_exchanges_funded_is_positive
        CHECK (funded_at IS NULL OR difference_due > 0);

-- The status carries its moment, as 'completed' and 'canceled' already do.
ALTER TABLE order_exchanges
    ADD CONSTRAINT order_exchanges_funded_status_stamp
        CHECK (status <> 'funded' OR funded_at IS NOT NULL);

-- The completion's bound, widened by exactly one case and no more. It replaces
-- 000017's rule rather than sitting beside it: an exchange completes when it
-- owes nothing, OR when what it owed was positive and has been funded.
--
-- A negative difference still cannot reach 'completed' from any direction,
-- which is what 000017 was protecting.
ALTER TABLE order_exchanges
    DROP CONSTRAINT IF EXISTS order_exchanges_completed_owes_nothing;

ALTER TABLE order_exchanges
    ADD CONSTRAINT order_exchanges_completed_is_settled
        CHECK (status <> 'completed'
               OR difference_due = 0
               OR (difference_due > 0 AND funded_at IS NOT NULL));

-- One collection funds one exchange. The index is the structural half of that
-- sentence: two flows racing to fund the same exchange, or one collection
-- named by two exchanges, lose in the database rather than in a read-then-write
-- that has no lock between its halves.
CREATE UNIQUE INDEX IF NOT EXISTS order_exchanges_payment_collection_uniq
    ON order_exchanges (payment_collection_id)
    WHERE payment_collection_id IS NOT NULL;
