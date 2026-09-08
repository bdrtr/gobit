-- The order module's six tables lose their deleted_at columns.
--
-- # Why the columns go rather than gaining a writer
--
-- AN ORDER RETIRES BY STATUS, and this module built that answer before it was
-- asked the question. Four statuses cover the whole lifecycle and every one of
-- the four transitions is dated: placed_at, completed_at, canceled_at with a
-- reason beside it, and archived_at. Two of them are held to their status by a
-- mirror CHECK (orders_canceled_stamp, orders_completed_stamp) and the third by
-- a one-directional one (orders_archived_stamp). 'archived' is precisely the
-- state a soft delete would have been asked for -- an order out of the daily
-- lists that is still a sale that happened -- and 000007 already made it a
-- dated transition rather than a hidden row.
--
-- The five child tables follow the order they hang from. A return, an exchange
-- and a claim each carry their own status with their own stamps; a line and a
-- return item are immutable once written, which order_line_items.sql states as
-- the reason it holds no UPDATE at all.
--
-- Nothing has ever written any of the six. Every read carried
-- "deleted_at IS NULL" -- 34 of the module's 52 statements -- and the predicate
-- has never once been false in a running shop. The six statements that leave it
-- out do so deliberately: ListOrdersForErasure and the five ForDisclosure reads
-- answer what the DATABASE STILL HOLDS about a person rather than what the shop
-- can see (ADR 0034), and both files argue that under a heading of their own.
-- Those readings do not change here; they simply stop being exceptions to a
-- rule that no longer exists.
--
-- # Why writing the column would have been the worse half
--
-- The header of 000001 states the trap in its own words: order_summaries has NO
-- deleted_at and GetOrderSummary never asks whether the order is alive, so
-- "WHEN soft deletion arrives for the order, this query must be bound too;
-- otherwise GetOrder says NotFound while GetOrderSummary returns a populated
-- record." That caution is answered here rather than carried further: soft
-- deletion is not arriving. A divergence of the same shape would open a second
-- time through the invoice, and that is THIS migration's inference and not a
-- standing one: ADR 0032 keeps an issued invoice through erasure and says
-- nothing about orders, so the document outliving a hidden order is a
-- consequence drawn here.
--
-- # The uniqueness rules, and a correction
--
-- fulfillment's 000003 removed a column of this shape and distinguished itself
-- from these ten with the sentence "D9's ten carry no such rule", meaning a
-- uniqueness rule written as "unique among LIVING rows". THAT SENTENCE IS
-- WRONG, and it was measured on 2026-09-08: four of the ten carry exactly that
-- rule -- orders_idempotency_key_uniq and order_return_items_line_uniq here,
-- payment_sessions_provider_idempotency_uniq and payments_session_uniq in the
-- payment module.
--
-- The consequence is the one fulfillment named. On a real PostgreSQL 16.14: one
-- hand-written UPDATE stamping an order lets the SAME idempotency key open a
-- SECOND live order, which is the collision orders_idempotency_key_uniq exists
-- to make impossible. The unconditional index below refuses that state instead.
--
-- # The indexes have to be rebuilt, and the drop is silent
--
-- PostgreSQL drops any index whose PREDICATE names a dropped column, with no
-- notice. Fifteen of this module's seventeen indexes are partial on deleted_at.
-- Measured on the repository's own PostgreSQL 16.14 before this file was
-- written: DROP COLUMN removed a UNIQUE partial index without a word, and the
-- duplicate key that had been impossible one statement earlier was accepted.
-- Dropping the columns without the statements below would take fifteen indexes
-- and leave the schema looking untouched.
--
-- If an installation HAS stamped the column by hand, this migration fails on
-- the unique index rather than dropping a guarantee quietly. That is the
-- intended direction: the state is unrepresentable from here on, and finding
-- out is better than being told nothing.
ALTER TABLE orders             DROP COLUMN IF EXISTS deleted_at;
ALTER TABLE order_line_items   DROP COLUMN IF EXISTS deleted_at;
ALTER TABLE order_returns      DROP COLUMN IF EXISTS deleted_at;
ALTER TABLE order_return_items DROP COLUMN IF EXISTS deleted_at;
ALTER TABLE order_claims       DROP COLUMN IF EXISTS deleted_at;
ALTER TABLE order_exchanges    DROP COLUMN IF EXISTS deleted_at;

-- The idempotency key is now unique among ALL orders rather than among living
-- ones, which is what Principle 2.6 always meant: there is no way to stop being
-- an order.
CREATE UNIQUE INDEX IF NOT EXISTS orders_idempotency_key_uniq
    ON orders (idempotency_key)
    WHERE idempotency_key IS NOT NULL;

-- Renamed from orders_alive_idx. The old name described the PREDICATE that no
-- longer exists, and "alive" on an index over every row is the next reader's
-- wrong assumption. 000006's comment tells them apart by the columns they carry,
-- and that distinction is untouched: this one orders by created_at, the moment
-- the ROW was written, and orders_placed_at_idx by the moment the SALE happened.
CREATE INDEX IF NOT EXISTS orders_listing_idx
    ON orders (created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS orders_customer_idx
    ON orders (customer_id)
    WHERE customer_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS orders_region_idx  ON orders (region_id);
CREATE INDEX IF NOT EXISTS orders_status_idx  ON orders (status);

CREATE INDEX IF NOT EXISTS orders_cart_idx
    ON orders (cart_id)
    WHERE cart_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS orders_placed_at_idx
    ON orders (placed_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS order_line_items_order_idx
    ON order_line_items (order_id, created_at, id);

CREATE INDEX IF NOT EXISTS order_line_items_variant_idx
    ON order_line_items (variant_id, order_id);

CREATE INDEX IF NOT EXISTS order_returns_order_idx
    ON order_returns (order_id, created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS order_exchanges_order_idx
    ON order_exchanges (order_id, created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS order_claims_order_idx
    ON order_claims (order_id, created_at DESC, id DESC);

-- A line appears AT MOST ONCE in one return, now without the escape a stamped
-- row used to be.
CREATE UNIQUE INDEX IF NOT EXISTS order_return_items_line_uniq
    ON order_return_items (order_return_id, order_line_item_id);

CREATE INDEX IF NOT EXISTS order_return_items_return_idx
    ON order_return_items (order_return_id, created_at, id);

CREATE INDEX IF NOT EXISTS order_return_items_line_idx
    ON order_return_items (order_line_item_id);
