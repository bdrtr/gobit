-- Schema of the promotion module (plan Phase 7, Section 6).
--
-- The tables belong to THIS module alone. Principle 2.2 forbids a REFERENCES
-- to another module's table: which order a redemption belongs to is held as
-- FREE text in promotion_redemption.reference and is not a foreign key. Foreign
-- keys between the module's OWN tables are unconstrained, and they are used.
--
-- Time columns are TIMESTAMPTZ and are always written in UTC; deletion is SOFT
-- (deleted_at) and every read query applies the deleted_at IS NULL filter.
--
-- Money is an INTEGER minor unit and rates are BASIS POINTS (10000 = 100%); no
-- column anywhere uses floating point (plan Section 8).

-- campaign is the container promotions sit in: a shared date window and a
-- shared budget.
--
-- The budget is measured in one of two units: "spend" (money, minor unit) or
-- "usage" (a count). A currency exists ONLY for "spend" and there it is
-- MANDATORY; a campaign with no budget cannot carry a limit either. Three
-- constraints lock those three rules at the database level, so that not even a
-- maintenance script running SQL directly can leave an inconsistent budget
-- behind.
CREATE TABLE IF NOT EXISTS campaign (
    id                   TEXT PRIMARY KEY,
    name                 TEXT        NOT NULL,
    campaign_identifier  TEXT        NOT NULL,
    description          TEXT        NOT NULL DEFAULT '',
    starts_at            TIMESTAMPTZ,
    ends_at              TIMESTAMPTZ,
    budget_type          TEXT        NOT NULL DEFAULT 'none',
    budget_limit         BIGINT,
    budget_used          BIGINT      NOT NULL DEFAULT 0,
    budget_currency_code TEXT,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at           TIMESTAMPTZ,
    CONSTRAINT campaign_name_check       CHECK (name <> ''),
    CONSTRAINT campaign_identifier_check CHECK (campaign_identifier <> ''),
    CONSTRAINT campaign_window_check     CHECK (starts_at IS NULL OR ends_at IS NULL OR starts_at < ends_at),
    CONSTRAINT campaign_budget_type_check CHECK (budget_type IN ('none', 'spend', 'usage')),
    CONSTRAINT campaign_budget_limit_check CHECK (
        budget_limit IS NULL OR (budget_limit >= 0 AND budget_limit <= 1000000000000)
    ),
    CONSTRAINT campaign_budget_used_check CHECK (budget_used >= 0),
    -- A CASE is used and NOT "A OR B": in SQL a comparison with NULL yields
    -- NULL, and a CHECK constraint counts a NULL result as PASSING. A
    -- constraint shaped as "budget_type = 'spend' AND budget_currency_code ~
    -- '...'" returns NULL when the currency is NULL, and would SILENTLY accept
    -- the inconsistent row.
    CONSTRAINT campaign_budget_currency_check CHECK (
        CASE budget_type
            WHEN 'spend' THEN budget_currency_code IS NOT NULL
                              AND budget_currency_code ~ '^[A-Z]{3}$'
            ELSE budget_currency_code IS NULL
        END
    ),
    CONSTRAINT campaign_budget_none_check CHECK (budget_type <> 'none' OR budget_limit IS NULL)
);

-- The business identifier is unique among LIVE records. The partial index is
-- deliberate: the identifier of a soft-deleted campaign has to be reusable,
-- otherwise a deleted campaign's name would stay reserved for ever.
CREATE UNIQUE INDEX IF NOT EXISTS campaign_identifier_uniq
    ON campaign (campaign_identifier) WHERE deleted_at IS NULL;

-- promotion is a single discount definition.
--
-- code is both the coupon code and the name an operator refers to the promotion
-- by; it is unique among live records and is always stored in UPPER case
-- (coupon codes must not distinguish case — "yaz20" and "YAZ20" are the same
-- coupon).
--
-- campaign_id is ON DELETE SET NULL: when a campaign is deleted its promotions
-- are NOT deleted, they merely lose their container. A deletion must not
-- destroy the promotion history.
CREATE TABLE IF NOT EXISTS promotion (
    id           TEXT PRIMARY KEY,
    code         TEXT        NOT NULL,
    is_automatic BOOLEAN     NOT NULL DEFAULT FALSE,
    type         TEXT        NOT NULL,
    campaign_id  TEXT        REFERENCES campaign(id) ON DELETE SET NULL,
    status       TEXT        NOT NULL,
    usage_limit  BIGINT,
    usage_count  BIGINT      NOT NULL DEFAULT 0,
    metadata     JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at   TIMESTAMPTZ,
    CONSTRAINT promotion_code_check        CHECK (code <> '' AND code = upper(code)),
    CONSTRAINT promotion_type_check        CHECK (type IN ('standard', 'buyget')),
    CONSTRAINT promotion_status_check      CHECK (status IN ('draft', 'active', 'inactive')),
    CONSTRAINT promotion_usage_limit_check CHECK (usage_limit IS NULL OR usage_limit >= 0),
    CONSTRAINT promotion_usage_count_check CHECK (usage_count >= 0),
    CONSTRAINT promotion_metadata_check    CHECK (jsonb_typeof(metadata) = 'object')
);

CREATE UNIQUE INDEX IF NOT EXISTS promotion_code_uniq
    ON promotion (code) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS promotion_campaign_idx
    ON promotion (campaign_id) WHERE deleted_at IS NULL;
-- Every computation round scans the "active and automatic" promotions; the
-- partial index makes that scan independent of the table's size.
CREATE INDEX IF NOT EXISTS promotion_automatic_idx
    ON promotion (id) WHERE deleted_at IS NULL AND is_automatic AND status = 'active';

-- promotion_application_method is HOW the discount is applied.
--
-- A promotion has at most ONE method (the partial unique index). More than one
-- would lead to an ambiguous state in which the same promotion produces two
-- different discounts.
--
-- The value field measures two different things depending on the type: in
-- "fixed" it is an amount in minor units, in "percentage" it is basis points.
-- That is why the upper bounds are split into type-dependent constraints; a
-- percentage cannot exceed 10000 basis points (100%).
CREATE TABLE IF NOT EXISTS promotion_application_method (
    id            TEXT PRIMARY KEY,
    promotion_id  TEXT        NOT NULL REFERENCES promotion(id) ON DELETE CASCADE,
    type          TEXT        NOT NULL,
    target_type   TEXT        NOT NULL,
    allocation    TEXT        NOT NULL,
    value         BIGINT      NOT NULL,
    max_quantity  BIGINT,
    currency_code TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at    TIMESTAMPTZ,
    CONSTRAINT promotion_application_method_type_check   CHECK (type IN ('fixed', 'percentage')),
    CONSTRAINT promotion_application_method_target_check CHECK (target_type IN ('items', 'shipping_methods', 'order')),
    CONSTRAINT promotion_application_method_alloc_check  CHECK (allocation IN ('each', 'across')),
    CONSTRAINT promotion_application_method_value_check  CHECK (value >= 0),
    -- A CASE is used and NOT "A OR B": the argument stands beside
    -- campaign_budget_currency_check — a NULL currency would collapse the
    -- constraint to NULL and let the row through silently.
    CONSTRAINT promotion_application_method_currency_check CHECK (
        CASE type
            WHEN 'fixed' THEN currency_code IS NOT NULL AND currency_code ~ '^[A-Z]{3}$'
            ELSE currency_code IS NULL
        END
    ),
    CONSTRAINT promotion_application_method_fixed_check CHECK (
        type <> 'fixed' OR value <= 1000000000000
    ),
    CONSTRAINT promotion_application_method_pct_check CHECK (
        type <> 'percentage' OR value <= 10000
    ),
    CONSTRAINT promotion_application_method_maxqty_check CHECK (
        max_quantity IS NULL OR (max_quantity >= 1 AND max_quantity <= 1000000)
    ),
    -- An order target spreads ONE single total across the line items; "each"
    -- is meaningless there.
    CONSTRAINT promotion_application_method_order_alloc_check CHECK (
        target_type <> 'order' OR allocation = 'across'
    )
);

CREATE UNIQUE INDEX IF NOT EXISTS promotion_application_method_promotion_uniq
    ON promotion_application_method (promotion_id) WHERE deleted_at IS NULL;

-- promotion_rule is the condition under which a promotion applies.
--
-- rule_type says WHAT the condition looks at: "context" looks at the cart's
-- context (currency, region, customer group), "target" at the line item's own
-- attributes.
--
-- The column is NOT named "values": VALUES is a reserved word in PostgreSQL and
-- could not be used unquoted (the same argument as in pricing.price_rule).
CREATE TABLE IF NOT EXISTS promotion_rule (
    id           TEXT PRIMARY KEY,
    promotion_id TEXT        NOT NULL REFERENCES promotion(id) ON DELETE CASCADE,
    rule_type    TEXT        NOT NULL,
    attribute    TEXT        NOT NULL,
    operator     TEXT        NOT NULL,
    rule_values  TEXT[]      NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at   TIMESTAMPTZ,
    CONSTRAINT promotion_rule_type_check      CHECK (rule_type IN ('context', 'target')),
    CONSTRAINT promotion_rule_attribute_check CHECK (attribute <> ''),
    CONSTRAINT promotion_rule_operator_check  CHECK (operator IN ('eq', 'ne', 'in', 'nin', 'gt', 'gte', 'lt', 'lte')),
    CONSTRAINT promotion_rule_values_check    CHECK (array_length(rule_values, 1) >= 1)
);

CREATE INDEX IF NOT EXISTS promotion_rule_promotion_idx
    ON promotion_rule (promotion_id) WHERE deleted_at IS NULL;

-- promotion_redemption is the LEDGER of the usage counters.
--
-- The counters themselves are the promotion.usage_count and
-- campaign.budget_used columns; this table records for which reference and by
-- how much they were raised, and it makes two things possible: idempotent
-- redemption (the same reference does not raise the counter a second time) and
-- exact reversal (budget_delta is subtracted as it stands, never guessed).
--
-- reference is an order/cart identifier but it is NOT a foreign key: that
-- record belongs to another module (Principle 2.2).
-- currency_code has NO DEFAULT and cannot be left empty: every amount in the
-- ledger MUST CARRY the currency it is denominated in (plan Section 8). A row
-- without one does not say which currency the campaign budget was consumed in,
-- and on release it would be subtracted back with that same ambiguity.
CREATE TABLE IF NOT EXISTS promotion_redemption (
    id            TEXT PRIMARY KEY,
    promotion_id  TEXT        NOT NULL REFERENCES promotion(id) ON DELETE CASCADE,
    campaign_id   TEXT        REFERENCES campaign(id) ON DELETE SET NULL,
    reference     TEXT        NOT NULL,
    amount        BIGINT      NOT NULL DEFAULT 0,
    currency_code TEXT        NOT NULL,
    budget_delta  BIGINT      NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    released_at   TIMESTAMPTZ,
    CONSTRAINT promotion_redemption_reference_check CHECK (reference <> ''),
    CONSTRAINT promotion_redemption_amount_check    CHECK (amount >= 0 AND amount <= 1000000000000),
    CONSTRAINT promotion_redemption_delta_check     CHECK (budget_delta >= 0),
    CONSTRAINT promotion_redemption_currency_check  CHECK (currency_code ~ '^[A-Z]{3}$')
);

-- For one reference there can be only one VALID redemption AT A TIME.
-- The partial index is deliberate: the reference of a released redemption can
-- be used again (release → redeem again), but two valid redemptions cannot
-- raise the counter twice. Of two concurrent Redeems, one hits this index.
CREATE UNIQUE INDEX IF NOT EXISTS promotion_redemption_active_uniq
    ON promotion_redemption (promotion_id, reference) WHERE released_at IS NULL;
CREATE INDEX IF NOT EXISTS promotion_redemption_promotion_idx
    ON promotion_redemption (promotion_id);
