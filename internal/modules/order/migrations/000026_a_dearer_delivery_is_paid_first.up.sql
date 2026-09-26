-- A dearer delivery is paid before it changes (ADR 0200).
--
-- 000025 wrote only changes that cost the same or less, and held that with a
-- CHECK, because nothing could take the difference. A change that costs more
-- now names the payment collection that took it: the identifier and not the
-- amount, for the reason 000018 gives an exchange's funding (ADR 0119). The
-- difference beside it is this module's own figure, the price of one service
-- less the price of the other.

ALTER TABLE order_delivery_changes
    ADD COLUMN IF NOT EXISTS payment_collection_id TEXT;

ALTER TABLE order_delivery_changes
    DROP CONSTRAINT IF EXISTS order_delivery_changes_costs_no_more;

-- A collection exactly when the change costs more. A dearer change with no
-- collection is a delivery nobody paid for, and a collection on a change that
-- costs no more is money taken for nothing.
ALTER TABLE order_delivery_changes
    ADD CONSTRAINT order_delivery_changes_paid_when_dearer
        CHECK ((difference > 0) = (payment_collection_id IS NOT NULL));

-- One collection pays for one change. The order module checks an exchange's
-- funding against this column under the order's lock; this index is what
-- holds two changes apart.
CREATE UNIQUE INDEX IF NOT EXISTS order_delivery_changes_payment_collection_uniq
    ON order_delivery_changes (payment_collection_id)
    WHERE payment_collection_id IS NOT NULL;

-- The order journal reads a window of dearer changes.
CREATE INDEX IF NOT EXISTS order_delivery_changes_dearer_idx
    ON order_delivery_changes (created_at, id)
    WHERE difference > 0;
