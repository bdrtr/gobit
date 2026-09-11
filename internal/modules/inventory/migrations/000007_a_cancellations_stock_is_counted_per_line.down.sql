-- The reverse of 000007, in the reverse order.
--
-- The unique index comes back FIRST, because it can only be created while the
-- data still satisfies it — and after this migration has been live, it may not:
-- a line whose target grew twice carries two cancellation movements with the same
-- reference. A `down` that fails loudly there is the correct outcome. It is the
-- shape this repository accepts for a rollback that cannot be silently true, and
-- the operator's way out is to reconcile the duplicate references by hand.
CREATE UNIQUE INDEX IF NOT EXISTS inventory_movements_cancellation_once_idx
    ON inventory_movements (reference)
    WHERE reason = 'cancellation';

DROP INDEX IF EXISTS inventory_movements_cancellation_line_idx;

ALTER TABLE inventory_movements
    DROP CONSTRAINT IF EXISTS inventory_movements_line_not_blank;
ALTER TABLE inventory_movements
    DROP CONSTRAINT IF EXISTS inventory_movements_line_only_on_cancellation;
ALTER TABLE inventory_movements
    DROP COLUMN IF EXISTS line_item_id;
