-- Schema of the order module (plan Phase 6).
--
-- Ownership: the six tables here belong ONLY to the order module. Intra-module
-- foreign keys are free and are used (the line items, the summary and the
-- return/exchange/claim records are bound to the order with ON DELETE CASCADE);
-- NO REFERENCES IS GIVEN to another module's table (Principle 2.2 — the
-- cross-module FK ban). That is why orders.region_id, orders.customer_id,
-- orders.cart_id and order_line_items.variant_id are free TEXT: the relation is
-- established over Module Links.
--
-- Money: ALL amounts are BIGINT and in minor units (cents); the currency sits in
-- a separate column (plan Section 8). Floating point is used nowhere.
--
-- Time: every stamp is timestamptz (UTC). Deletion is soft (deleted_at) and the
-- orders/order_line_items read queries apply the deleted_at IS NULL filter. In
-- Phase 6 there is NO surface that DELETES an order.
--
-- CAUTION — order_summaries IS OUTSIDE THIS RULE: the table HAS NO deleted_at
-- column and GetOrderSummary never asks whether the order is alive. The only
-- reason this is harmless today is that no deleting surface exists. WHEN soft
-- deletion arrives for the order, this query must be bound too (JOIN orders +
-- deleted_at IS NULL); otherwise GetOrder says NotFound while GetOrderSummary
-- returns a populated record.

-- orders is an order.
--
-- # Why the database produces display_id
--
-- display_id is the human-readable INCREASING number shown to the customer
-- ("your order number 1042"). Had it been produced in the application layer with
-- "read the largest, add one, write it", two concurrent orders would take the
-- SAME number: between the read and the write the second operation steps in and
-- both rows would compute the same MAX+1 value. A row lock would not have been
-- enough either — there is no COMMON row to lock, both of them open a NEW row.
--
-- That is why an IDENTITY column (that is, a sequence) produces the number. The
-- sequence advances atomically, outside the transaction; two concurrent INSERTs
-- cannot see each other's value and cannot take the same number. GENERATED
-- ALWAYS is the chosen form: had it been BY DEFAULT, an INSERT could write the
-- column explicitly, skip the sequence and pierce the guarantee.
-- orders_display_id_uniq is then the last defense — even if the sequence is
-- rewound by hand (setval), the collision is caught before a row is written.
--
-- The sequence is born together with the column and falls together with DROP
-- TABLE; a separate CREATE/DROP SEQUENCE is not needed.
--
-- # Total fields
--
-- The order totals are copied from the cart's SNAPSHOT and never change again:
-- the order is the permanent answer to the question "what was sold at that
-- moment and what did it come to". The identity constraint
-- (orders_totals_consistent) prevents a saga step's wrong arithmetic from being
-- silently written to the order; the service performs the same check earlier
-- with a more readable error, and the constraint here is the last defense —
-- it also covers an intervention made directly with SQL.
CREATE TABLE IF NOT EXISTS orders (
    id              TEXT        PRIMARY KEY,
    -- display_id is the human-readable INCREASING number shown to the customer.
    display_id      BIGINT      NOT NULL GENERATED ALWAYS AS IDENTITY,
    -- status is the order's place in its lifecycle.
    status          TEXT        NOT NULL DEFAULT 'pending',
    -- region_id is the region module's id; there is NO FK (Principle 2.2).
    region_id       TEXT        NOT NULL,
    -- customer_id is the customer module's id; if NULL the order is a GUEST's.
    customer_id     TEXT,
    email           TEXT,
    -- currency_code is the ISO 4217 code and is stored in UPPERCASE.
    currency_code   TEXT        NOT NULL,
    -- cart_id is the cart the order was born from; it is the cart module's id
    -- and is NOT an FK. It documents only the ORIGIN; it is not used for reads.
    cart_id         TEXT,
    -- idempotency_key prevents the same order from being written twice
    -- (Principle 2.6). A saga may retry a step; without the key a retry would
    -- have meant opening a SECOND ORDER for the customer.
    idempotency_key TEXT,
    subtotal        BIGINT      NOT NULL DEFAULT 0,
    discount_total  BIGINT      NOT NULL DEFAULT 0,
    tax_total       BIGINT      NOT NULL DEFAULT 0,
    shipping_total  BIGINT      NOT NULL DEFAULT 0,
    total           BIGINT      NOT NULL DEFAULT 0,
    metadata        JSONB       NOT NULL DEFAULT '{}'::jsonb,
    -- placed_at is the moment the order was placed; the birth moment of the
    -- order record.
    placed_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at    TIMESTAMPTZ,
    canceled_at     TIMESTAMPTZ,
    cancel_reason   TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at      TIMESTAMPTZ,

    CONSTRAINT orders_status_valid
        CHECK (status IN ('pending', 'completed', 'archived', 'canceled')),
    CONSTRAINT orders_subtotal_nonneg       CHECK (subtotal >= 0),
    CONSTRAINT orders_discount_total_nonneg CHECK (discount_total >= 0),
    CONSTRAINT orders_tax_total_nonneg      CHECK (tax_total >= 0),
    CONSTRAINT orders_shipping_total_nonneg CHECK (shipping_total >= 0),
    CONSTRAINT orders_total_nonneg          CHECK (total >= 0),
    CONSTRAINT orders_totals_consistent
        CHECK (total = subtotal - discount_total + tax_total + shipping_total),
    -- The discount CANNOT EXCEED the subtotal: had it exceeded, the customer
    -- would win back more than the goods they bought, and the tax and the
    -- shipping would be covered by the discount.
    CONSTRAINT orders_discount_within_subtotal
        CHECK (discount_total <= subtotal),
    -- The status and the stamp are each other's MIRROR; their divergence meant a
    -- record that looks canceled but has no cancellation moment (or the reverse).
    CONSTRAINT orders_canceled_stamp
        CHECK ((status = 'canceled') = (canceled_at IS NOT NULL)),
    CONSTRAINT orders_completed_stamp
        CHECK ((status IN ('completed', 'archived')) = (completed_at IS NOT NULL))
);

-- display_id uniqueness is the LAST DEFENSE: the sequence already does not
-- collide, but if the sequence is rewound by hand (setval) or a record is
-- copied, the same number cannot be written a second time.
CREATE UNIQUE INDEX IF NOT EXISTS orders_display_id_uniq
    ON orders (display_id);

CREATE INDEX IF NOT EXISTS orders_alive_idx
    ON orders (created_at DESC, id DESC)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS orders_customer_idx
    ON orders (customer_id)
    WHERE customer_id IS NOT NULL AND deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS orders_region_idx
    ON orders (region_id)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS orders_status_idx
    ON orders (status)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS orders_cart_idx
    ON orders (cart_id)
    WHERE cart_id IS NOT NULL AND deleted_at IS NULL;

-- A SECOND order cannot be opened with the same idempotency key. A soft-deleted
-- order is outside the constraint: the key of a deleted order must be reusable
-- again, otherwise the delete operation would consume the key forever.
CREATE UNIQUE INDEX IF NOT EXISTS orders_idempotency_key_uniq
    ON orders (idempotency_key)
    WHERE idempotency_key IS NOT NULL AND deleted_at IS NULL;

-- order_line_items is a line in the order.
--
-- variant_id is the product module's id and is NOT a FOREIGN KEY (Principle
-- 2.2). title and unit_price are COPIED as well: even if the catalog changes
-- later (or the variant is deleted), the name and the amount seen on the invoice
-- do not change.
--
-- A uniqueness constraint on (order_id, variant_id) is DELIBERATELY ABSENT (the
-- cart has one). An order is a historical record and in later phases an exchange
-- may add a second line for the same variant; the cart's "one line per variant"
-- rule, on the other hand, arises from the cart being editable. The protection
-- against the same order being written twice is not at the line level but at the
-- order level, with orders_idempotency_key_uniq.
CREATE TABLE IF NOT EXISTS order_line_items (
    id             TEXT        PRIMARY KEY,
    order_id       TEXT        NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    -- variant_id is the product module's id; there is NO FK (Principle 2.2).
    variant_id     TEXT        NOT NULL,
    title          TEXT        NOT NULL,
    quantity       BIGINT      NOT NULL,
    unit_price     BIGINT      NOT NULL DEFAULT 0,
    subtotal       BIGINT      NOT NULL DEFAULT 0,
    discount_total BIGINT      NOT NULL DEFAULT 0,
    tax_total      BIGINT      NOT NULL DEFAULT 0,
    total          BIGINT      NOT NULL DEFAULT 0,
    metadata       JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at     TIMESTAMPTZ,

    CONSTRAINT order_line_items_quantity_positive     CHECK (quantity > 0),
    CONSTRAINT order_line_items_unit_price_nonneg     CHECK (unit_price >= 0),
    CONSTRAINT order_line_items_subtotal_nonneg       CHECK (subtotal >= 0),
    CONSTRAINT order_line_items_discount_total_nonneg CHECK (discount_total >= 0),
    CONSTRAINT order_line_items_tax_total_nonneg      CHECK (tax_total >= 0),
    CONSTRAINT order_line_items_total_nonneg          CHECK (total >= 0),
    -- Shipping does not exist at the line level; it belongs to the whole order.
    CONSTRAINT order_line_items_totals_consistent
        CHECK (total = subtotal - discount_total + tax_total),
    CONSTRAINT order_line_items_discount_within_subtotal
        CHECK (discount_total <= subtotal)
);

CREATE INDEX IF NOT EXISTS order_line_items_order_idx
    ON order_line_items (order_id, created_at, id)
    WHERE deleted_at IS NULL;

-- order_summaries is the order's paid/refunded/outstanding amount summary.
--
-- It is a SINGLE row per order and is born zeroed together with the order:
-- there is no such state as "an order without a summary", so the reading side is
-- not forced to tell NULL apart from zero.
--
-- The OUTSTANDING amount is NOT STORED as a column; it is read as
-- total - (paid_total - refunded_total) (see models.OrderSummary.Outstanding).
-- Had it been stored, the consistency of the three columns with one another
-- would have to be protected by a separate constraint, and a derived value could
-- go stale.
--
-- The side that WRITES the paid amount is NOT the payment module: the two
-- modules do not know each other (Principle 2.1). The write reaches this
-- module's service over the workflow that knows the payment result, or over an
-- event subscriber.
CREATE TABLE IF NOT EXISTS order_summaries (
    id             TEXT        PRIMARY KEY,
    order_id       TEXT        NOT NULL UNIQUE REFERENCES orders (id) ON DELETE CASCADE,
    -- paid_total is the total amount CAPTURED against the order (minor unit).
    paid_total     BIGINT      NOT NULL DEFAULT 0,
    -- refunded_total is the total amount PAID BACK to the customer (minor unit).
    refunded_total BIGINT      NOT NULL DEFAULT 0,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT order_summaries_paid_total_nonneg     CHECK (paid_total >= 0),
    CONSTRAINT order_summaries_refunded_total_nonneg CHECK (refunded_total >= 0),
    -- An amount that was never captured CANNOT be refunded.
    CONSTRAINT order_summaries_refund_within_paid
        CHECK (refunded_total <= paid_total)
);

-- order_returns is the SKELETON of a return record (plan Section 6).
--
-- Phase 6 sets up only the record and its basic CRUD; the return workflow
-- (line-based return, stock restoration, payment refund) belongs to later
-- phases. That is why a line-based child table IS NOT THERE YET: a child schema
-- designed before the workflow is written would most likely change when the flow
-- arrives, and would leave behind a migration that has to be rolled back.
CREATE TABLE IF NOT EXISTS order_returns (
    id            TEXT        PRIMARY KEY,
    order_id      TEXT        NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    status        TEXT        NOT NULL DEFAULT 'requested',
    -- refund_amount is the amount planned to be refunded (minor unit).
    refund_amount BIGINT      NOT NULL DEFAULT 0,
    reason        TEXT,
    note          TEXT,
    metadata      JSONB       NOT NULL DEFAULT '{}'::jsonb,
    received_at   TIMESTAMPTZ,
    canceled_at   TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at    TIMESTAMPTZ,

    CONSTRAINT order_returns_status_valid
        CHECK (status IN ('requested', 'received', 'canceled')),
    CONSTRAINT order_returns_refund_amount_nonneg CHECK (refund_amount >= 0)
);

CREATE INDEX IF NOT EXISTS order_returns_order_idx
    ON order_returns (order_id, created_at DESC, id DESC)
    WHERE deleted_at IS NULL;

-- order_exchanges is the SKELETON of an exchange record (plan Section 6).
--
-- difference_due CAN BE NEGATIVE and that is why it has no nonneg constraint: in
-- an exchange the difference may be captured from the customer just as it may be
-- paid to the customer. The amount is still an INTEGER minor unit (plan
-- Section 8); the sign states the direction, not the scale.
CREATE TABLE IF NOT EXISTS order_exchanges (
    id             TEXT        PRIMARY KEY,
    order_id       TEXT        NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    status         TEXT        NOT NULL DEFAULT 'requested',
    -- difference_due is paid by the customer when positive, and to the customer
    -- when negative.
    difference_due BIGINT      NOT NULL DEFAULT 0,
    note           TEXT,
    metadata       JSONB       NOT NULL DEFAULT '{}'::jsonb,
    completed_at   TIMESTAMPTZ,
    canceled_at    TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at     TIMESTAMPTZ,

    CONSTRAINT order_exchanges_status_valid
        CHECK (status IN ('requested', 'completed', 'canceled'))
);

CREATE INDEX IF NOT EXISTS order_exchanges_order_idx
    ON order_exchanges (order_id, created_at DESC, id DESC)
    WHERE deleted_at IS NULL;

-- order_claims is the SKELETON of a damage/shortage record (plan Section 6).
--
-- claim_type says how the claim will be met: 'refund' is a money refund,
-- 'replace' is replacing the product with a new one.
CREATE TABLE IF NOT EXISTS order_claims (
    id            TEXT        PRIMARY KEY,
    order_id      TEXT        NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    claim_type    TEXT        NOT NULL,
    status        TEXT        NOT NULL DEFAULT 'requested',
    -- refund_amount is the amount to refund when claim_type = 'refund'.
    refund_amount BIGINT      NOT NULL DEFAULT 0,
    reason        TEXT,
    note          TEXT,
    metadata      JSONB       NOT NULL DEFAULT '{}'::jsonb,
    completed_at  TIMESTAMPTZ,
    canceled_at   TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at    TIMESTAMPTZ,

    CONSTRAINT order_claims_type_valid
        CHECK (claim_type IN ('refund', 'replace')),
    CONSTRAINT order_claims_status_valid
        CHECK (status IN ('requested', 'completed', 'canceled')),
    CONSTRAINT order_claims_refund_amount_nonneg CHECK (refund_amount >= 0)
);

CREATE INDEX IF NOT EXISTS order_claims_order_idx
    ON order_claims (order_id, created_at DESC, id DESC)
    WHERE deleted_at IS NULL;
