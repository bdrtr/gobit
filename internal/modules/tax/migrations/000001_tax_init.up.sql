-- Schema of the tax module (plan Phase 7, Section 6).
--
-- Ownership: the three tables here belong to the tax module ALONE. Foreign keys
-- INSIDE the module are free and are used (a rate belongs to a region, a rule
-- to a rate); NO REFERENCES IS GIVEN to another module's table (Principle 2.2 —
-- the cross-module FK ban). That is why tax_rate_rule.reference_id is free
-- TEXT: it is the id of a product, a product type or a shipping option, and the
-- existence of those records is not verified HERE.
--
-- The rate: rate_bps is in BASIS POINTS (2000 = 20%) and is an INTEGER. Plan
-- Section 8 bans floats for money and its derivatives; the float form of 20%
-- (0.2), multiplied by an amount, would produce silent rounding at the cent
-- level. The unit at the end of the column name is deliberate — with "rate" it
-- would stay unclear whether the value 20 means 20% or 0.2.
--
-- Time: every stamp is TIMESTAMPTZ (UTC). Deletion is SOFT (deleted_at) and
-- every read query filters on deleted_at IS NULL.

-- tax_region is a tax region: a country root, or a province under that root.
--
-- # Why the hierarchy is two levels
--
-- Tax geography is in practice two levels: the country (VAT) and the
-- sub-country unit (a US state, a Canadian province, a TR province). parent_id
-- gives that link to the table's OWN rows; a deeper tree could be modelled, but
-- the calculation path deliberately resolves two levels (see
-- service/calculate.go). Limiting the depth is what prevents the calculation
-- from turning into a recursive query whose cost cannot be predicted.
--
-- # The "at most one root region per country" rule
--
-- The rule is enforced IN THE DATABASE, by the tax_region_country_root_uniq
-- partial unique index. The service makes the same check first, with a more
-- readable error, but the last defense is here: two concurrent requests can
-- pass the service's "read first, then write" check together and write two root
-- regions for one country. From that moment on, which rate applies would be
-- left to row order.
--
-- # Why a province and its root's country cannot diverge
--
-- parent_id is bound to the parent row NOT ON ITS OWN, but as the PAIR
-- (parent_id, country_code); and its target is the (id, country_code)
-- uniqueness. The consequence: a province row's country CANNOT DIFFER from its
-- parent's country. A single-column FK leaves this free, and a record such as
-- "a DE province under the TR root" could come into being silently; the
-- calculation would look for that province in Germany and never find it.
--
-- # Why provider_id is INHERITED
--
-- An empty provider_id does not mean "local", it means "my parent's provider":
-- the calculation walks the chain from the most specific to the general and
-- uses the first NON-EMPTY value, and if none of them is filled it falls back
-- to local calculation (see service.Service.providerFor). The rule closes a
-- money bug — while the country is bound to an external authority, a province
-- row opened for a single exception would, because its field is left empty,
-- silently tax EVERY cart in that province out of the local table. A filled
-- value calls the external provider with that id; if the provider is not
-- registered the calculation does NOT fall back to local SILENTLY, it returns
-- an error.
--
-- tax_region_provider_id_check keeps the field TRIMMED and BOUNDED. Two
-- reasons: because the registry lookup trims the id before searching, an
-- untrimmed value would produce a divergence between what is STORED and what is
-- APPLIED; and an unbounded text field is the cheapest way to write megabytes
-- of data into a table with a single request. The service applies the same rule
-- first with a readable error; this constraint covers direct SQL as well.
CREATE TABLE IF NOT EXISTS tax_region (
    id            TEXT PRIMARY KEY,
    -- country_code is the ISO 3166-1 alpha-2 code; always stored UPPERCASE.
    country_code  TEXT        NOT NULL,
    -- province_code is the code of the sub-country unit; NULL on a root region.
    province_code TEXT,
    -- parent_id is the root region; NULL on a root row.
    parent_id     TEXT,
    -- provider_id is the id of the tax provider; when empty the parent's
    -- provider is inherited, and on a root row local calculation applies.
    provider_id   TEXT        NOT NULL DEFAULT '',
    metadata      JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at    TIMESTAMPTZ,

    CONSTRAINT tax_region_country_code_check
        CHECK (country_code ~ '^[A-Z]{2}$'),
    CONSTRAINT tax_region_province_code_check
        CHECK (province_code IS NULL OR province_code ~ '^[A-Z0-9][A-Z0-9-]{0,9}$'),
    CONSTRAINT tax_region_provider_id_check
        CHECK (provider_id = btrim(provider_id, E' \t\n\r\v\f') AND length(provider_id) <= 255),
    -- A province region MUST HAVE its root, and a root region MUST NOT carry a
    -- province code; the two are born together or not at all. Otherwise a
    -- "province with no parent" (a record that is never found) or a "root
    -- carrying a province code" (a rate applied to a single province instead of
    -- the whole country) would be possible.
    CONSTRAINT tax_region_hierarchy_check
        CHECK ((parent_id IS NULL AND province_code IS NULL)
            OR (parent_id IS NOT NULL AND province_code IS NOT NULL)),
    CONSTRAINT tax_region_self_parent_check
        CHECK (parent_id IS NULL OR parent_id <> id),
    -- The target of the composite FK; id is already the primary key, and this
    -- constraint only supplies a combination the (parent_id, country_code)
    -- reference can bind to.
    CONSTRAINT tax_region_id_country_uniq UNIQUE (id, country_code),
    CONSTRAINT tax_region_parent_fk
        FOREIGN KEY (parent_id, country_code) REFERENCES tax_region (id, country_code)
);

-- A country has AT MOST one root tax region.
CREATE UNIQUE INDEX IF NOT EXISTS tax_region_country_root_uniq
    ON tax_region (country_code)
    WHERE parent_id IS NULL AND deleted_at IS NULL;

-- The same province code cannot appear twice under one root. The index is at
-- the same time the read path of the (country, province) resolution.
CREATE UNIQUE INDEX IF NOT EXISTS tax_region_province_uniq
    ON tax_region (parent_id, province_code)
    WHERE parent_id IS NOT NULL AND deleted_at IS NULL;

-- tax_rate is a rate within a tax region.
--
-- is_default is the region's DEFAULT rate: every line item that matches no rule
-- falls to it. A region can have at most one default rate, and the rule is
-- enforced by the tax_rate_default_uniq partial unique index — a second default
-- would leave which rate applies to row order.
--
-- code is for reconciliation with external systems (e.g. "VAT20") and may be
-- NULL. Being nullable is deliberate: telling "no code" apart with an empty
-- string would mean two empty codes collide in the uniqueness index. When a
-- code is given, it is unique within the region.
CREATE TABLE IF NOT EXISTS tax_rate (
    id            TEXT PRIMARY KEY,
    tax_region_id TEXT        NOT NULL,
    name          TEXT        NOT NULL,
    code          TEXT,
    -- rate_bps is the rate in BASIS POINTS: 2000 = 20%, 10000 = 100%.
    rate_bps      INTEGER     NOT NULL DEFAULT 0,
    is_default    BOOLEAN     NOT NULL DEFAULT FALSE,
    metadata      JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at    TIMESTAMPTZ,

    CONSTRAINT tax_rate_name_check CHECK (name <> ''),
    CONSTRAINT tax_rate_code_check CHECK (code IS NULL OR code <> ''),
    CONSTRAINT tax_rate_bps_check  CHECK (rate_bps >= 0 AND rate_bps <= 10000),
    CONSTRAINT tax_rate_region_fk
        FOREIGN KEY (tax_region_id) REFERENCES tax_region (id)
);

CREATE UNIQUE INDEX IF NOT EXISTS tax_rate_default_uniq
    ON tax_rate (tax_region_id)
    WHERE is_default AND deleted_at IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS tax_rate_code_uniq
    ON tax_rate (tax_region_id, code)
    WHERE code IS NOT NULL AND deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS tax_rate_region_idx
    ON tax_rate (tax_region_id)
    WHERE deleted_at IS NULL;

-- tax_rate_rule says WHICH line item a rate applies to.
--
-- reference carries the type of the line item, reference_id the id within that
-- type. The id belongs to other modules (product, fulfillment) and IS NOT A FK
-- (Principle 2.2): tax does not know those records, it only looks at id
-- equality. A deleted product's rule can therefore be left behind; that is
-- harmless, because no line item with that id enters a calculation any more.
--
-- A default rate HAS NO rule: if "a rate without rules applies to everything"
-- and "a rate with rules applies only to what matches" were joined in one row,
-- the scope of the rate would become unreadable. The rule is enforced in the
-- service, not in the database (a CHECK that looks at two tables at once cannot
-- be written); see service/rule.go.
CREATE TABLE IF NOT EXISTS tax_rate_rule (
    id           TEXT PRIMARY KEY,
    tax_rate_id  TEXT        NOT NULL,
    reference    TEXT        NOT NULL,
    reference_id TEXT        NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at   TIMESTAMPTZ,

    CONSTRAINT tax_rate_rule_reference_check
        CHECK (reference IN ('product', 'product_type', 'shipping_option')),
    CONSTRAINT tax_rate_rule_reference_id_check CHECK (reference_id <> ''),
    CONSTRAINT tax_rate_rule_rate_fk
        FOREIGN KEY (tax_rate_id) REFERENCES tax_rate (id)
);

CREATE UNIQUE INDEX IF NOT EXISTS tax_rate_rule_uniq
    ON tax_rate_rule (tax_rate_id, reference, reference_id)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS tax_rate_rule_rate_idx
    ON tax_rate_rule (tax_rate_id)
    WHERE deleted_at IS NULL;
