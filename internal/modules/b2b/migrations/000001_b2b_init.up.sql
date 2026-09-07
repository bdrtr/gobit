-- The b2b module's schema.
--
-- The tables belong to THIS MODULE ALONE and their names start with the "b2b_"
-- prefix. Principle 2.2 forbids a REFERENCES to another module's table: the
-- employee is tied to her CUSTOMER record through core/link, and this schema
-- carries no customer_id column at all (the argument is in
-- internal/modules/b2b/service, Definitions). A foreign key between the
-- module's OWN tables is free, and one is used on the b2b_company_employee ->
-- b2b_company edge.
--
-- Time columns are TIMESTAMPTZ and are always written in UTC; deletion is SOFT
-- (deleted_at) and every read query filters deleted_at IS NULL. Money is an
-- INTEGER count of minor units (plan Section 8).

-- b2b_company is the company that shops on behalf of a LEGAL ENTITY rather
-- than on behalf of an individual.
--
-- The e-mail is stored normalized to LOWER case (enforced by a CHECK) but it is
-- NOT UNIQUE. Uniqueness was deliberately left out: no identity is established
-- by e-mail in this module (sign-in lives on the customer/auth side), while by
-- contrast two legal entities of the same holding may perfectly well share one
-- accounting address. A unique index would reject a real record for the sake of
-- an identity that does not exist. The price of this is that the e-mail filter
-- may return more than one row, and that is written into the filter's contract.
--
-- The address fields MAY BE LEFT EMPTY: a company record is most often opened
-- before the invoicing address is settled. The currency, by contrast, is
-- mandatory — a spending limit (b2b_company_employee.spending_limit) is an
-- integer and cannot be compared without knowing which currency it is in.
CREATE TABLE IF NOT EXISTS b2b_company (
    id                            TEXT PRIMARY KEY,
    name                          TEXT        NOT NULL,
    email                         TEXT        NOT NULL,
    phone                         TEXT        NOT NULL DEFAULT '',
    address                       TEXT        NOT NULL DEFAULT '',
    city                          TEXT        NOT NULL DEFAULT '',
    postal_code                   TEXT        NOT NULL DEFAULT '',
    country_code                  TEXT        NOT NULL DEFAULT '',
    currency_code                 TEXT        NOT NULL,
    spending_limit_reset_period   TEXT        NOT NULL DEFAULT 'never',
    created_at                    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                    TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at                    TIMESTAMPTZ,
    CONSTRAINT b2b_company_name_check     CHECK (name <> '' AND length(name) <= 255),
    CONSTRAINT b2b_company_email_check    CHECK (email <> '' AND email = lower(email) AND length(email) <= 320),
    CONSTRAINT b2b_company_currency_check CHECK (currency_code ~ '^[A-Z]{3}$'),
    -- An empty country code means "the address has not been entered yet"; once
    -- it is entered it MUST be ISO 3166-1 alpha-2. Expressing both cases in a
    -- single constraint is what the "the address is optional" decision looks
    -- like in the database.
    CONSTRAINT b2b_company_country_check  CHECK (country_code = '' OR country_code ~ '^[A-Z]{2}$'),
    -- The reset period is an ENUM and its set of values lives IN THE SCHEMA.
    -- The application layer validates it as well; but the next step, the one
    -- that will enforce the spending limit, reads this column and branches on
    -- it, and for it to see a value it does not recognize would mean "the limit
    -- was never enforced at all".
    CONSTRAINT b2b_company_reset_check    CHECK (spending_limit_reset_period IN ('monthly', 'yearly', 'never'))
);

-- Filtering by e-mail (the administration listing) uses this index. It is NOT
-- unique; the argument for that is in the table's documentation.
CREATE INDEX IF NOT EXISTS b2b_company_email_idx
    ON b2b_company (email)
    WHERE deleted_at IS NULL;

-- b2b_company_employee is the employee who may spend on the company's behalf.
--
-- The employee's tie to her CUSTOMER record is NOT here, it is in the
-- "b2b_employee_customer" link (core/link). A customer_id column would mean
-- holding the same relation in two places: every divergence between the column
-- and the link would produce two different answers to the storefront's "which
-- is my own employee record" question. The single source is the link table, and
-- it is the only way from a customer to her employee record.
--
-- If spending_limit is NULL the employee may spend WITHOUT LIMIT; if it is 0
-- she may spend nothing. Separating the two is essential — had a single zero
-- value been used, "I set no limit" and "I zeroed the limit" would land on the
-- same row.
CREATE TABLE IF NOT EXISTS b2b_company_employee (
    id               TEXT        PRIMARY KEY,
    company_id       TEXT        NOT NULL REFERENCES b2b_company(id) ON DELETE CASCADE,
    spending_limit   BIGINT,
    is_company_admin BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at       TIMESTAMPTZ,
    -- A negative limit is not a bound but a meaningless number: every
    -- comparison would exceed it and the employee would silently become unable
    -- to shop at all.
    CONSTRAINT b2b_company_employee_limit_check CHECK (spending_limit IS NULL OR spending_limit >= 0)
);

-- Listing a company's employees is the most frequent read there is; if
-- company_id is left unindexed every listing falls back to a table scan.
CREATE INDEX IF NOT EXISTS b2b_company_employee_company_idx
    ON b2b_company_employee (company_id)
    WHERE deleted_at IS NULL;
