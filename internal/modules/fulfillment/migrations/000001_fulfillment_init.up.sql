-- Schema of the fulfillment module (plan Phase 7).
--
-- Ownership: the six tables here belong ONLY to the fulfillment module.
-- Intra-module foreign keys are free and are used; NO REFERENCES IS GIVEN to
-- another module's table (Principle 2.2 — the cross-module FK ban). That is why
-- shipping_options.region_id (the region module's id), fulfillments.reference
-- (the order id) and fulfillment_items.line_item_id (the order line id) are NOT
-- FKs: the relation is established over Module Links and the link table lives
-- in the core.
--
-- Money: every amount is BIGINT and in minor units (cents); the currency sits
-- in a SEPARATE column (plan Section 8). NUMERIC or floating point is used
-- nowhere — the cent of the shipping fee enters the order total exactly.
--
-- Time: every stamp is timestamptz (UTC). Deletion is soft AS A RULE
-- (deleted_at) and every read query on those tables applies the
-- deleted_at IS NULL filter. The exceptions are countable and each one's
-- rationale sits at the head of its own table:
--
--   - fulfillment_items — a shipment's line does not live apart from the
--     shipment; when the shipment is canceled the line is not "deleted", the
--     shipment's status changes.
--   - fulfillment_manual_shipments — not the module's domain data but the
--     ledger of the imitated external system.
--
-- 000002 adds two more exceptions (shipping_locations and
-- shipping_location_regions); their rationales sit at the head of that file.

-- shipping_profiles is the shipping profile: the container that groups which
-- products are subject to which shipping rules.
--
-- Products are bound to profiles with Module Links; the profile DOES NOT KNOW
-- which products are bound to it (Principle 2.1). There is one "default"
-- profile in every store and it is where products bind by default; "gift_card"
-- is reserved for products that require no physical shipment.
CREATE TABLE IF NOT EXISTS shipping_profiles (
    id         TEXT        PRIMARY KEY,
    name       TEXT        NOT NULL,
    type       TEXT        NOT NULL DEFAULT 'default',
    metadata   JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ,

    CONSTRAINT shipping_profiles_name_check CHECK (name <> ''),
    CONSTRAINT shipping_profiles_type_valid CHECK (type IN ('default', 'gift_card', 'custom'))
);

-- A profile name is unique among LIVING records: two profiles carrying the same
-- name means the administrator cannot tell which rule they are editing.
CREATE UNIQUE INDEX IF NOT EXISTS shipping_profiles_name_uniq
    ON shipping_profiles (name)
    WHERE deleted_at IS NULL;

-- shipping_options is a shipping option: the source of the rows offered to the
-- customer, such as "Standard shipping", "Express shipping", "Pick up in store".
--
-- price_type takes two values:
--   flat       — the fee is the amount on this row; the provider is NEVER
--                called.
--   calculated — the fee is determined by the provider's Quote; amount is
--                unused and MUST be zero (the constraint below). A price with
--                two sources would leave it to the reader to work out which
--                one holds.
--
-- data is the CONFIGURATION belonging to the provider (e.g. the fee per
-- kilogram) and is passed to the Quote call as is; metadata, in turn, is the
-- store's own free-form data. The two are separate because data DOES NOT
-- SURFACE on the storefront.
CREATE TABLE IF NOT EXISTS shipping_options (
    id                  TEXT        PRIMARY KEY,
    name                TEXT        NOT NULL,
    provider_id         TEXT        NOT NULL,
    shipping_profile_id TEXT        NOT NULL REFERENCES shipping_profiles (id) ON DELETE CASCADE,
    price_type          TEXT        NOT NULL DEFAULT 'flat',
    amount              BIGINT      NOT NULL DEFAULT 0,
    currency_code       TEXT        NOT NULL,
    -- region_id is the region module's id; there is NO FK (Principle 2.2).
    -- The empty string means "every region".
    region_id           TEXT        NOT NULL DEFAULT '',
    is_return           BOOLEAN     NOT NULL DEFAULT FALSE,
    admin_only          BOOLEAN     NOT NULL DEFAULT FALSE,
    data                JSONB       NOT NULL DEFAULT '{}'::jsonb,
    metadata            JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at          TIMESTAMPTZ,

    CONSTRAINT shipping_options_name_check      CHECK (name <> ''),
    CONSTRAINT shipping_options_provider_check  CHECK (provider_id <> ''),
    CONSTRAINT shipping_options_price_type_valid CHECK (price_type IN ('flat', 'calculated')),
    -- A zero amount is valid and means "free shipping"; a negative amount would
    -- mean paying the customer money for the shipping.
    CONSTRAINT shipping_options_amount_nonneg   CHECK (amount >= 0),
    CONSTRAINT shipping_options_amount_max      CHECK (amount <= 1000000000000),
    -- On a calculated option the amount comes from the provider; the value on
    -- the row would be a STALE second source. The service already rejects this;
    -- the constraint here is the last defense and stops an intervention made
    -- directly with SQL as well.
    CONSTRAINT shipping_options_calculated_zero CHECK (price_type <> 'calculated' OR amount = 0),
    CONSTRAINT shipping_options_currency_format CHECK (currency_code ~ '^[A-Z]{3}$')
);

CREATE INDEX IF NOT EXISTS shipping_options_region_idx
    ON shipping_options (region_id, currency_code)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS shipping_options_profile_idx
    ON shipping_options (shipping_profile_id)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS shipping_options_alive_idx
    ON shipping_options (created_at DESC, id DESC)
    WHERE deleted_at IS NULL;

-- shipping_option_rules determines UNDER WHICH CONDITION an option is offered.
--
-- The condition is the triple (attribute, operator, rule_values); e.g.
-- ("subtotal", "gte", {"50000"}) — "free shipping once the subtotal passes
-- 50,000 cents". The column is NOT named "values": VALUES is a reserved word in
-- PostgreSQL and could not have been used unquoted (the same rationale as
-- price_rule in the pricing module).
--
-- ALL rules of an option must match; an option without rules is unconditional.
CREATE TABLE IF NOT EXISTS shipping_option_rules (
    id                 TEXT        PRIMARY KEY,
    shipping_option_id TEXT        NOT NULL REFERENCES shipping_options (id) ON DELETE CASCADE,
    attribute          TEXT        NOT NULL,
    operator           TEXT        NOT NULL,
    rule_values        TEXT[]      NOT NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at         TIMESTAMPTZ,

    CONSTRAINT shipping_option_rules_attribute_check CHECK (attribute <> ''),
    CONSTRAINT shipping_option_rules_operator_check
        CHECK (operator IN ('eq', 'ne', 'in', 'nin', 'gt', 'gte', 'lt', 'lte')),
    -- cardinality returns 0 for an empty array, not NULL; had it been written
    -- with array_length the result would be NULL and a CHECK returning NULL
    -- would count as SATISFIED — the constraint would in effect have prevented
    -- nothing (see pricing 000002).
    CONSTRAINT shipping_option_rules_values_check CHECK (cardinality(rule_values) >= 1)
);

CREATE INDEX IF NOT EXISTS shipping_option_rules_option_idx
    ON shipping_option_rules (shipping_option_id)
    WHERE deleted_at IS NULL;

-- fulfillments is a shipment that has happened.
--
-- reference is the id of the caller's own record (the order). There is NO FK
-- (Principle 2.2); the bond is established with Module Links.
--
-- external_id is the provider's own shipment id; it is the field that matches
-- the two systems during reconciliation. Because the shipment row is written
-- BEFORE GOING to the provider it is empty at the start and is filled in after
-- the provider's response.
CREATE TABLE IF NOT EXISTS fulfillments (
    id                 TEXT        PRIMARY KEY,
    reference          TEXT        NOT NULL,
    shipping_option_id TEXT        NOT NULL REFERENCES shipping_options (id) ON DELETE RESTRICT,
    provider_id        TEXT        NOT NULL,
    external_id        TEXT        NOT NULL DEFAULT '',
    status             TEXT        NOT NULL DEFAULT 'pending',
    tracking_number    TEXT        NOT NULL DEFAULT '',
    tracking_url       TEXT        NOT NULL DEFAULT '',
    -- idempotency_key prevents the same shipment from being created twice
    -- (plan Section 2.6). A repeat without the key would mean A SECOND SHIPPING
    -- LABEL.
    idempotency_key    TEXT        NOT NULL,
    shipped_at         TIMESTAMPTZ,
    delivered_at       TIMESTAMPTZ,
    canceled_at        TIMESTAMPTZ,
    data               JSONB       NOT NULL DEFAULT '{}'::jsonb,
    metadata           JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at         TIMESTAMPTZ,

    CONSTRAINT fulfillments_reference_check CHECK (reference <> ''),
    CONSTRAINT fulfillments_provider_check  CHECK (provider_id <> ''),
    CONSTRAINT fulfillments_key_check       CHECK (idempotency_key <> ''),
    CONSTRAINT fulfillments_status_valid
        CHECK (status IN ('pending', 'shipped', 'delivered', 'canceled')),
    -- Status and the timestamps must agree with each other: the dispatch moment
    -- of a "shipped" shipment, the delivery moment of a "delivered" one and the
    -- cancellation moment of a "canceled" one must have been WRITTEN. A status
    -- without a stamp would leave the question "when?" unanswered during
    -- reconciliation.
    CONSTRAINT fulfillments_shipped_stamp   CHECK (status <> 'shipped'   OR shipped_at   IS NOT NULL),
    CONSTRAINT fulfillments_delivered_stamp CHECK (status <> 'delivered' OR delivered_at IS NOT NULL),
    CONSTRAINT fulfillments_canceled_stamp  CHECK (status <> 'canceled'  OR canceled_at  IS NOT NULL)
);

-- An idempotency key is unique among LIVING shipments. When the saga retries a
-- step the second Create hits this index with ON CONFLICT DO NOTHING, writes no
-- row and reads the existing shipment; the index is the single point of the race.
CREATE UNIQUE INDEX IF NOT EXISTS fulfillments_idempotency_uniq
    ON fulfillments (idempotency_key)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS fulfillments_reference_idx
    ON fulfillments (reference, created_at DESC)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS fulfillments_alive_idx
    ON fulfillments (created_at DESC, id DESC)
    WHERE deleted_at IS NULL;

-- fulfillment_items are the lines that go into the shipment.
--
-- line_item_id is the id of the order line; there is NO FK (Principle 2.2) and
-- it is not validated in this module. The quantity is BIGINT and positive.
CREATE TABLE IF NOT EXISTS fulfillment_items (
    id             TEXT        PRIMARY KEY,
    fulfillment_id TEXT        NOT NULL REFERENCES fulfillments (id) ON DELETE CASCADE,
    line_item_id   TEXT        NOT NULL,
    quantity       BIGINT      NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT fulfillment_items_line_check     CHECK (line_item_id <> ''),
    -- The UPPER bound of the quantity is in the schema too. The service already
    -- applies the same bound (models.MaxQuantity), but the application layer is
    -- not the last defense on its own: the quantity is a number MULTIPLIED by a
    -- fee and an unbounded value can overflow the product out of int64 and turn
    -- it into a negative amount. A maintenance script running SQL directly trips
    -- on this constraint as well.
    CONSTRAINT fulfillment_items_quantity_check CHECK (quantity > 0),
    CONSTRAINT fulfillment_items_quantity_max   CHECK (quantity <= 1000000)
);

-- The same order line cannot appear TWICE in one shipment; two rows would leave
-- it to the reader to work out which quantity holds.
CREATE UNIQUE INDEX IF NOT EXISTS fulfillment_items_line_uniq
    ON fulfillment_items (fulfillment_id, line_item_id);

-- fulfillment_manual_shipments is the MANUAL provider's own ledger.
--
-- Why a SEPARATE table: the manual provider IMITATES a real shipping company.
-- A real provider's state lives in its own system and the module reaches it only
-- through the FulfillmentProvider interface. Preserving the same separation here
-- structurally prevents the module from accidentally reading the provider's
-- internal state: the fulfillment service NEVER touches this table.
--
-- Why NOT IN MEMORY: the same rationale as the manual provider in the payment
-- module. When the process restarts, a shipment that was created must still be
-- findable; the saga compensation (Cancel) has to run in exactly the scenario
-- where the process fell over, and more than one process must see the same
-- shipment.
--
-- There is NO soft deletion: this table is not the module's domain data but the
-- ledger of the imitated external system; its records are never deleted.
CREATE TABLE IF NOT EXISTS fulfillment_manual_shipments (
    id              TEXT        PRIMARY KEY,
    idempotency_key TEXT        NOT NULL,
    reference       TEXT        NOT NULL,
    option_id       TEXT        NOT NULL,
    status          TEXT        NOT NULL DEFAULT 'pending',
    tracking_number TEXT        NOT NULL DEFAULT '',
    tracking_url    TEXT        NOT NULL DEFAULT '',
    data            JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT fulfillment_manual_shipments_reference_check CHECK (reference <> ''),
    CONSTRAINT fulfillment_manual_shipments_key_check       CHECK (idempotency_key <> ''),
    CONSTRAINT fulfillment_manual_shipments_status_valid
        CHECK (status IN ('pending', 'shipped', 'delivered', 'canceled'))
);

-- The same idempotency key cannot open a SECOND shipment. This is the constraint
-- that ultimately enforces the idempotency requirement of the provider contract
-- (core/provider).
CREATE UNIQUE INDEX IF NOT EXISTS fulfillment_manual_shipments_idempotency_uniq
    ON fulfillment_manual_shipments (idempotency_key);
