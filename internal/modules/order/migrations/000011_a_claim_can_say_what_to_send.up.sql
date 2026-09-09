-- order_replacements is WHAT a claim promises to send, and it is a record
-- before it is a shipment.
--
-- # What was missing
--
-- internal/workflows/returns/claim.go refuses to settle a claim of type
-- 'replace' because "shipping a replacement against an existing order is not
-- something this framework can do yet". The sentence is true, and the first
-- thing missing is not the shipping: it is that NOTHING SAYS WHAT TO SEND.
-- order_claims carries a type and a refund amount; order_exchanges carries a
-- difference. Neither carries an item.
--
-- An order cannot carry it either. The order is immutable — "after an order is
-- written its AMOUNTS and its LINES do not change" (models.Order) — so the
-- replacement is a record BESIDE it, like the return and the claim before it.
--
-- # Why the status vocabulary is TWO words and not five
--
-- The obvious design gives this table the whole journey: requested, held,
-- dispatching, dispatched. Every one of those but the first needs stock to move
-- or a parcel to exist, and this change does neither — it lets a claim SAY what
-- to send and nothing more.
--
-- Shipping a status whose code path does not exist is the mistake 000008 was
-- written to undo: order_exchanges carried completed_at and a 'completed' value
-- from the day it was created and nothing could ever write either, so both were
-- dropped rather than kept as a promise. The same rule applies to a table being
-- born: the vocabulary says what can happen, and today what can happen is that
-- a replacement is asked for and withdrawn.
--
-- Adding the rest is a migration and a cheap one — 000008 says so in its own
-- last paragraph — and it arrives with the flow that fills it.
--
-- # There is no exchange source
--
-- An exchange could own a replacement too, and it will. But no path in this
-- change writes one, and a nullable source with no writer is the column class
-- this repository keeps finding in itself (D2, D4). One source now, and the
-- second when the flow that needs it lands.
--
-- # There is no idempotency key
--
-- The row IS the key. A retry that opens the parcel names this record, so the
-- shipment's idempotency key is derived from the id rather than stored beside
-- it; a second column holding the same fact could disagree with it.
--
-- # There is no deleted_at
--
-- 000010 took those columns out of this module's six tables and gave the reason:
-- a record retires by STATUS, dated. A table born after that decision does not
-- get to reintroduce the shape it removed.
CREATE TABLE IF NOT EXISTS order_replacements (
    id                 TEXT        PRIMARY KEY,
    order_claim_id     TEXT        NOT NULL REFERENCES order_claims (id) ON DELETE CASCADE,
    status             TEXT        NOT NULL DEFAULT 'requested',
    -- shipping_option_id and location_id say HOW and FROM WHERE, and they are
    -- answered when the replacement is asked for rather than when it ships: a
    -- retry has to read them from the row instead of trusting a repeated body,
    -- or two calls with different options would each believe they were honoured.
    -- Neither carries a foreign key; both belong to other modules
    -- (Principle 2.2).
    shipping_option_id TEXT        NOT NULL,
    location_id        TEXT        NOT NULL,
    note               TEXT,
    canceled_at        TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT order_replacements_status_valid
        CHECK (status IN ('requested', 'canceled')),
    -- The mirror form: the status and its moment imply each other in BOTH
    -- directions. It is addable here for the reason 000008 could add it to
    -- order_exchanges and 000007 could not to orders — the table is new, so no
    -- row exists that carries one without the other.
    CONSTRAINT order_replacements_canceled_stamp
        CHECK ((status = 'canceled') = (canceled_at IS NOT NULL)),
    CONSTRAINT order_replacements_option_check   CHECK (shipping_option_id <> ''),
    CONSTRAINT order_replacements_location_check CHECK (location_id <> '')
);

CREATE INDEX IF NOT EXISTS order_replacements_claim_idx
    ON order_replacements (order_claim_id, created_at DESC, id DESC);

-- order_replacement_items is WHICH lines are being replaced, and how many of
-- each.
--
-- The shape is order_return_items' and the reasons carry over unchanged: the
-- line already holds the variant and the order line is immutable, so the row
-- JOINS rather than duplicates; the quantity carries the count, so one line
-- appears at most once.
--
-- What is NOT carried over is the refund amount. A replacement is settled with
-- goods, not money — order_claims already requires a 'replace' claim's refund
-- to be zero — and a per-line column that could only ever hold zero is a column
-- nothing reads.
--
-- The rule ACROSS replacements — that the quantities sent for a line cannot
-- exceed what was sold on it — is not here, because a CHECK cannot see another
-- row. It lives in the service under the order's lock, beside the same rule for
-- returns.
CREATE TABLE IF NOT EXISTS order_replacement_items (
    id                     TEXT        PRIMARY KEY,
    order_replacement_id   TEXT        NOT NULL
        REFERENCES order_replacements (id) ON DELETE CASCADE,
    order_line_item_id     TEXT        NOT NULL
        REFERENCES order_line_items (id) ON DELETE CASCADE,
    quantity               BIGINT      NOT NULL,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT order_replacement_items_quantity_positive CHECK (quantity > 0),
    CONSTRAINT order_replacement_items_quantity_max      CHECK (quantity <= 1000000)
);

CREATE UNIQUE INDEX IF NOT EXISTS order_replacement_items_line_uniq
    ON order_replacement_items (order_replacement_id, order_line_item_id);
