-- A replacement may be sourced from an EXCHANGE, and an exchange that owes
-- nothing can be completed.
--
-- # What changed since 000008
--
-- 000008 dropped the exchange's completion and named exactly what would bring it
-- back: "Completing an exchange needs BOTH of those -- goods out and, when the
-- difference is positive, money in -- and the framework has neither." One of the
-- two arrived: ADR 0090 built the goods-out flow, and order_replacements is the
-- record it sends against. It could only be fed by a CLAIM, because on the day it
-- was written a claim was the only thing that could ask for goods.
--
-- The other half did not arrive. The order-to-payment link is still one-to-one,
-- so money cannot be collected against an existing order, and an exchange with a
-- difference still cannot be settled inside this framework.
--
-- So completion comes back for the case where BOTH conditions hold: the goods
-- left, and there is no money to move. That condition is row-local, which means
-- the database can hold it rather than the service alone.

-- The source of a replacement is a claim OR an exchange, and exactly one of them.
ALTER TABLE order_replacements
    ALTER COLUMN order_claim_id DROP NOT NULL;

ALTER TABLE order_replacements
    ADD COLUMN order_exchange_id TEXT REFERENCES order_exchanges (id) ON DELETE CASCADE;

-- A record with neither source hangs off nothing and could not be read back to
-- an order; a record with both would answer "which one settled it?" twice.
ALTER TABLE order_replacements
    ADD CONSTRAINT order_replacements_one_source
        CHECK ((order_claim_id IS NULL) <> (order_exchange_id IS NULL));

CREATE INDEX IF NOT EXISTS order_replacements_exchange_idx
    ON order_replacements (order_exchange_id, created_at DESC, id DESC);

-- The exchange gets its completion back, with the stamp and the bound.
ALTER TABLE order_exchanges
    ADD COLUMN completed_at TIMESTAMPTZ;

ALTER TABLE order_exchanges
    DROP CONSTRAINT IF EXISTS order_exchanges_status_valid;

ALTER TABLE order_exchanges
    ADD CONSTRAINT order_exchanges_status_valid
        CHECK (status IN ('requested', 'completed', 'canceled'));

-- The mirror form, as on order_returns and order_replacements: the status and
-- its moment imply each other in both directions.
ALTER TABLE order_exchanges
    ADD CONSTRAINT order_exchanges_completed_stamp
        CHECK ((status = 'completed') = (completed_at IS NOT NULL));

-- The bound that keeps 'completed' honest. An exchange with a difference is
-- money owed in a direction this framework cannot move, and marking it settled
-- would say a balance was handled when nothing handled it. The rule is a CHECK
-- rather than a service rule alone because it needs only this row -- which is
-- the whole reason it can be one.
ALTER TABLE order_exchanges
    ADD CONSTRAINT order_exchanges_completed_owes_nothing
        CHECK (status <> 'completed' OR difference_due = 0);
