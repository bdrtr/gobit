-- The pricing module's schema (plan Phase 4, Section 6).
--
-- The tables belong to THIS module alone. Per Principle 2.2 no REFERENCES is
-- given to another module's table: binding a variant to its price set is done
-- through Module Links, and pricing never sees that binding at all. Foreign
-- keys between the module's OWN tables are free, and are used.
--
-- The time columns are TIMESTAMPTZ and are always written in UTC; deletion is
-- SOFT (deleted_at) and every read query applies the deleted_at IS NULL filter.

-- price_list is a campaign/segment price list.
-- A price attached to a list is valid while the list's status and date window allow it.
CREATE TABLE IF NOT EXISTS price_list (
    id          TEXT PRIMARY KEY,
    title       TEXT        NOT NULL,
    description TEXT        NOT NULL DEFAULT '',
    type        TEXT        NOT NULL,
    status      TEXT        NOT NULL,
    starts_at   TIMESTAMPTZ,
    ends_at     TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at  TIMESTAMPTZ,
    CONSTRAINT price_list_title_check  CHECK (title <> ''),
    CONSTRAINT price_list_type_check   CHECK (type IN ('sale', 'override')),
    CONSTRAINT price_list_status_check CHECK (status IN ('draft', 'active', 'expired')),
    CONSTRAINT price_list_window_check CHECK (starts_at IS NULL OR ends_at IS NULL OR starts_at < ends_at)
);

-- price_set is the container holding a variant's prices.
-- The container itself does NOT know which variant it belongs to; the binding lives in the link table.
CREATE TABLE IF NOT EXISTS price_set (
    id         TEXT PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ
);

-- price is the amount for a single currency / quantity range.
--
-- amount is an INTEGER in minor units (cents); no float is used, and the
-- currency stands in its own column (plan Section 8). The upper bound is
-- deliberate: the product amount * max_quantity has to fit in an int64, or the
-- cart total would silently overflow.
CREATE TABLE IF NOT EXISTS price (
    id            TEXT PRIMARY KEY,
    price_set_id  TEXT        NOT NULL REFERENCES price_set(id) ON DELETE CASCADE,
    price_list_id TEXT        REFERENCES price_list(id) ON DELETE CASCADE,
    currency_code TEXT        NOT NULL,
    amount        BIGINT      NOT NULL,
    min_quantity  INTEGER     NOT NULL DEFAULT 1,
    max_quantity  INTEGER,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at    TIMESTAMPTZ,
    CONSTRAINT price_currency_check       CHECK (currency_code ~ '^[A-Z]{3}$'),
    CONSTRAINT price_amount_check         CHECK (amount >= 0 AND amount <= 1000000000000),
    CONSTRAINT price_min_quantity_check   CHECK (min_quantity >= 1 AND min_quantity <= 1000000),
    CONSTRAINT price_max_quantity_check   CHECK (max_quantity IS NULL OR max_quantity <= 1000000),
    CONSTRAINT price_quantity_range_check CHECK (max_quantity IS NULL OR max_quantity >= min_quantity)
);

CREATE INDEX IF NOT EXISTS price_set_id_idx ON price (price_set_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS price_list_id_idx ON price (price_list_id) WHERE deleted_at IS NULL;

-- price_rule states under which condition a price is valid.
--
-- The condition is the triple (attribute, operator, rule_values); e.g.
-- ("region_id", "eq", {"reg_1"}) or ("customer_group_id", "in", {"vip","b2b"}).
-- The column is NOT named "values": VALUES is a reserved word in PostgreSQL and
-- could not have been used unquoted.
CREATE TABLE IF NOT EXISTS price_rule (
    id          TEXT PRIMARY KEY,
    price_id    TEXT        NOT NULL REFERENCES price(id) ON DELETE CASCADE,
    attribute   TEXT        NOT NULL,
    operator    TEXT        NOT NULL,
    rule_values TEXT[]      NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at  TIMESTAMPTZ,
    CONSTRAINT price_rule_attribute_check CHECK (attribute <> ''),
    CONSTRAINT price_rule_operator_check  CHECK (operator IN ('eq', 'ne', 'in', 'nin', 'gt', 'gte', 'lt', 'lte')),
    CONSTRAINT price_rule_values_check    CHECK (array_length(rule_values, 1) >= 1)
);

CREATE INDEX IF NOT EXISTS price_rule_price_id_idx ON price_rule (price_id) WHERE deleted_at IS NULL;
