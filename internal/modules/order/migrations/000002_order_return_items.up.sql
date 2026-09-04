-- order_return_items is WHICH lines of the order are coming back, and how many
-- of each.
--
-- # Why the skeleton waited for this, and why it stops waiting now
--
-- 000001 deliberately left this table out, with the argument that "a child
-- schema designed before the workflow is written would most likely change when
-- the flow arrives". That argument was right and it is now spent: the return
-- flow is being written, and this schema is derived from the two things that
-- flow has to do — put stock back and pay money back.
--
-- Putting stock back needs the LINE and a QUANTITY. Paying money back needs an
-- AMOUNT. Nothing else about a returned line is needed by either, so nothing
-- else is here.
--
-- # There is no variant column
--
-- The line the row points at already carries the variant, and the order line is
-- immutable once written — the invoice must not change under the customer. A
-- copy here could only ever agree with it or be wrong, so the row joins rather
-- than duplicates. (The variant id itself has NO foreign key: it belongs to the
-- product module, Principle 2.2.)
--
-- # A line appears AT MOST ONCE in one return
--
-- The quantity carries the count, so two rows for the same line inside one
-- return would be two answers to a single question. The unique index makes that
-- unrepresentable rather than merely discouraged.
--
-- What it deliberately does NOT enforce is the rule ACROSS returns: that the
-- sum of every return's quantity for a line cannot exceed what was ordered. A
-- CHECK cannot see other rows, so that rule lives in the service, under the
-- order's lock, where it can be read and written atomically.
CREATE TABLE IF NOT EXISTS order_return_items (
    id                 TEXT        PRIMARY KEY,
    order_return_id    TEXT        NOT NULL REFERENCES order_returns (id) ON DELETE CASCADE,
    order_line_item_id TEXT        NOT NULL REFERENCES order_line_items (id) ON DELETE CASCADE,
    -- quantity is how many units of this line are coming back.
    quantity           BIGINT      NOT NULL,
    -- refund_amount is the part of the return's refund that falls on this line
    -- (minor unit). It is per-line so that a partial refund can say WHICH line
    -- it belongs to; the return's own refund_amount stays the total.
    refund_amount      BIGINT      NOT NULL DEFAULT 0,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at         TIMESTAMPTZ,

    CONSTRAINT order_return_items_quantity_positive  CHECK (quantity > 0),
    CONSTRAINT order_return_items_refund_nonneg      CHECK (refund_amount >= 0)
);

CREATE UNIQUE INDEX IF NOT EXISTS order_return_items_line_uniq
    ON order_return_items (order_return_id, order_line_item_id)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS order_return_items_return_idx
    ON order_return_items (order_return_id, created_at, id)
    WHERE deleted_at IS NULL;

-- The line index answers the ACROSS-returns question the service asks under the
-- lock: "how many of this line have already been asked back". Without it that
-- read is a scan of every return item in the installation.
CREATE INDEX IF NOT EXISTS order_return_items_line_idx
    ON order_return_items (order_line_item_id)
    WHERE deleted_at IS NULL;
