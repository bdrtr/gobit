-- The funding goes and the completion narrows back to an exchange that owes
-- nothing.
--
-- It REWRITES rather than refuses, which is 000017's test rather than 000008's:
-- 000008 refused because no code in this repository could have written the row
-- it found, so finding one was evidence of a hand-written record. A funded
-- exchange is the ordinary output of the feature, so finding one says nothing
-- except that the feature ran.
--
-- What the rollback loses is the LOCAL record of the funding: the moment and
-- the collection's identifier. The money itself is not lost and was never held
-- here -- it is in the payment module, in a collection that still exists with
-- its own captures and refunds, and it is still reachable by its reference.
-- What no longer exists after this is the sentence saying WHICH exchange that
-- collection answered.
--
-- Exchanges funded and completed are put back to 'requested' rather than left
-- as completions the narrowed CHECK would refuse. Their goods, if any left, are
-- still recorded by the replacement that sent them.

UPDATE order_exchanges
SET status       = 'requested',
    completed_at = NULL,
    funded_at    = NULL
WHERE status IN ('funded', 'completed')
  AND difference_due <> 0;

UPDATE order_exchanges
SET status    = 'requested',
    funded_at = NULL
WHERE status = 'funded';

DROP INDEX IF EXISTS order_exchanges_payment_collection_uniq;

ALTER TABLE order_exchanges
    DROP CONSTRAINT IF EXISTS order_exchanges_completed_is_settled,
    DROP CONSTRAINT IF EXISTS order_exchanges_funded_status_stamp,
    DROP CONSTRAINT IF EXISTS order_exchanges_funded_is_positive,
    DROP CONSTRAINT IF EXISTS order_exchanges_funded_stamp;

ALTER TABLE order_exchanges
    DROP CONSTRAINT IF EXISTS order_exchanges_status_valid;

ALTER TABLE order_exchanges
    ADD CONSTRAINT order_exchanges_status_valid
        CHECK (status IN ('requested', 'completed', 'canceled'));

ALTER TABLE order_exchanges
    ADD CONSTRAINT order_exchanges_completed_owes_nothing
        CHECK (status <> 'completed' OR difference_due = 0);

ALTER TABLE order_exchanges
    DROP COLUMN IF EXISTS funded_at,
    DROP COLUMN IF EXISTS payment_collection_id;
