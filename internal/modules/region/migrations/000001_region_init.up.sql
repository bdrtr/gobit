-- Schema of the region module (plan Phase 5, Section 6).
--
-- The tables belong to THIS MODULE ALONE. Principle 2.2 forbids a REFERENCES to
-- another module's table; binding a cart or an order to a region is done through
-- Module Links, and region never sees that link at all. Foreign keys between the
-- module's OWN tables are free and are used.
--
-- The time columns are TIMESTAMPTZ and are always written in UTC; deletion is
-- SOFT (deleted_at) and every read query applies a deleted_at IS NULL filter.
--
-- THAT RULE HOLDS FOR THE region TABLE ONLY, and that it does was understood
-- only after this file: 000003 DROPS the deleted_at columns of currency and
-- country. Both are REFERENCE DATA, their rows are written by the seed in
-- 000002, and their columns were never once written. The argument is at the top
-- of 000003; as far as those columns are concerned the two CREATE TABLEs below
-- are HISTORY, not the current schema.

-- currency is an ISO 4217 currency and it is REFERENCE DATA.
--
-- The primary key is not a generated identity but the code ITSELF: an ISO 4217
-- code is a global and immutable identifier, and inventing a second identity for
-- it would add one more translation step to every read. The code is always
-- stored in UPPER case; the service does the normalisation and the CHECK is the
-- second gate.
--
-- decimal_digits is this table's REASON FOR EXISTING: because money is stored as
-- an integer in the minor unit (cents) (plan Section 8), the presentation layer
-- learns its division factor here. TRY/USD have 2 digits, JPY 0, KWD 3;
-- assuming a fixed factor of 100 would show yen amounts a hundred times too
-- small.
CREATE TABLE IF NOT EXISTS currency (
    code           TEXT PRIMARY KEY,
    symbol         TEXT        NOT NULL,
    name           TEXT        NOT NULL,
    decimal_digits INTEGER     NOT NULL DEFAULT 2,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at     TIMESTAMPTZ,
    CONSTRAINT currency_code_check   CHECK (code ~ '^[A-Z]{3}$'),
    CONSTRAINT currency_name_check   CHECK (name <> ''),
    CONSTRAINT currency_symbol_check CHECK (symbol <> ''),
    CONSTRAINT currency_digits_check CHECK (decimal_digits >= 0 AND decimal_digits <= 4)
);

-- region is a sales region: a currency and (for the time being) a tax rate.
--
-- The currency_code foreign key is INSIDE the module and it is deliberate:
-- opening a region on an undefined currency would leave the currency of every
-- cart that falls into that region unresolvable. Deletion is restricted (the
-- default NO ACTION): a currency row that is in use cannot be deleted.
--
-- tax_rate is the FALLBACK rate (the tax module took this over, this is the
-- fallback path) and it is stored in BASIS POINTS: 2000 = 20%. The rate being an
-- integer is deliberate — plan Section 8 forbids floats for money and anything
-- derived from it, and the float form of a 20% rate (0.2) multiplied by an
-- amount would produce silent rounding at the cent level.
CREATE TABLE IF NOT EXISTS region (
    id              TEXT PRIMARY KEY,
    name            TEXT        NOT NULL,
    currency_code   TEXT        NOT NULL,
    automatic_taxes BOOLEAN     NOT NULL DEFAULT TRUE,
    tax_rate        INTEGER     NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at      TIMESTAMPTZ,
    CONSTRAINT region_name_check     CHECK (name <> ''),
    CONSTRAINT region_tax_rate_check CHECK (tax_rate >= 0 AND tax_rate <= 10000),
    CONSTRAINT region_currency_fk    FOREIGN KEY (currency_code) REFERENCES currency (code)
);

CREATE INDEX IF NOT EXISTS region_currency_code_idx ON region (currency_code) WHERE deleted_at IS NULL;

-- country is an ISO 3166-1 alpha-2 country and it is REFERENCE DATA.
--
-- The rule that a country belongs to AT MOST one region is STRUCTURAL: the link
-- is a single region_id column on the country row. Had a join table been used,
-- the same rule would have to be enforced separately by a unique index; one
-- column makes breaking it impossible in the schema itself.
--
-- region_id may be NULL: the whole of ISO 3166 is seeded, but an installation
-- sells to only a few of those. When a region is deleted the column is pulled
-- back to NULL (see repository.DeleteRegion); otherwise the country would stay
-- attached to a dead region and could never be added to any region again.
CREATE TABLE IF NOT EXISTS country (
    iso_2      TEXT PRIMARY KEY,
    name       TEXT        NOT NULL,
    region_id  TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ,
    CONSTRAINT country_iso_2_check CHECK (iso_2 ~ '^[A-Z]{2}$'),
    CONSTRAINT country_name_check  CHECK (name <> ''),
    CONSTRAINT country_region_fk   FOREIGN KEY (region_id) REFERENCES region (id)
);

CREATE INDEX IF NOT EXISTS country_region_id_idx ON country (region_id) WHERE deleted_at IS NULL;
