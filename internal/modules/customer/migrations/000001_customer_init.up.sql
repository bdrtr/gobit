-- The customer module's schema (plan Phase 5, Section 6).
--
-- The tables belong to THIS module ONLY. Per Principle 2.2, no REFERENCES is
-- given to another module's table: linking a cart or an order to a customer is
-- done through Module Links, and customer never sees that link. Foreign keys
-- BETWEEN the module's OWN tables are free and are used.
--
-- Time columns are TIMESTAMPTZ and are always written in UTC; deletion is SOFT
-- (deleted_at), and the read queries apply the deleted_at IS NULL filter.
--
-- The ONE exception to this filter is the ERASURE queries: the queries that
-- lock and overwrite a person's data do NOT look at deleted_at at all (see
-- queries/customer.sql, LockCustomerForErasure). The reason is a measurable
-- fact: a soft delete writes only deleted_at and updated_at and touches not a
-- single personal column — a deleted customer's e-mail, name and phone stay in
-- the table UNCHANGED. Had the filter been applied there too, a person whose
-- record was deleted would be told "your data has been anonymized" while their
-- data stayed where it was.
--
-- The same exception binds the index comments below: PARTIAL indexes built
-- WHERE deleted_at IS NULL CANNOT serve the erasure queries, because those
-- queries go outside the index's own condition.

-- customer holds both guest and registered customers; has_account tells them
-- apart.
--
-- The e-mail is always stored normalized to LOWER case. The normalization is
-- at storage because the uniqueness index is on the raw column: if "Ali@X.com"
-- and "ali@x.com" are to point at the same account, both have to come down to
-- the same bytes. The CHECK constraint keeps an e-mail with upper-case letters
-- out of the table even if the service's normalization is skipped.
CREATE TABLE IF NOT EXISTS customer (
    id          TEXT PRIMARY KEY,
    email       TEXT        NOT NULL,
    first_name  TEXT        NOT NULL DEFAULT '',
    last_name   TEXT        NOT NULL DEFAULT '',
    phone       TEXT        NOT NULL DEFAULT '',
    has_account BOOLEAN     NOT NULL DEFAULT FALSE,
    metadata    JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at  TIMESTAMPTZ,
    CONSTRAINT customer_email_check       CHECK (email <> '' AND email = lower(email)),
    CONSTRAINT customer_email_shape_check CHECK (email ~ '^[^[:space:]@]+@[^[:space:]@]+\.[^[:space:]@]+$'),
    CONSTRAINT customer_email_len_check   CHECK (length(email) <= 320)
);

-- The e-mail of registered ACCOUNTS is unique; that of guests is NOT.
--
-- The partial index is this module's most important decision. Guest orders
-- must be placeable with the same e-mail again and again: a customer typing
-- their address on the storefront cannot be refused because they shopped with
-- the same address months ago. On the other hand there cannot be two
-- REGISTERED accounts with the same e-mail, or the "sign in by e-mail" coming
-- in Phase 8 could not know which record to choose. The WHERE condition
-- expresses both requirements in one constraint and binds the rule to the
-- database rather than to the application.
--
-- The deleted_at IS NULL condition does a second job: a soft-deleted account's
-- e-mail stays reusable.
CREATE UNIQUE INDEX IF NOT EXISTS customer_account_email_uniq
    ON customer (email)
    WHERE has_account AND deleted_at IS NULL;

-- Lookups by e-mail (GetAccountByEmail, guest matching) use this index; the
-- partial unique index covers only accounts, so it is not enough for guest
-- lookups.
--
-- The ERASURE queries that look at the e-mail CANNOT USE this index: because of
-- the WHERE condition the index carries only live rows, while those queries
-- look for deleted rows too. They do a sequential scan (+ a sort), and this is
-- accepted knowingly; the reasoning is in the documentation of
-- repository/erasure.go, lockErasureTargets. Only the erasure query that
-- carries an ID alone works through the primary key and does not scan. The
-- query used when the subject carries BOTH an id AND an e-mail (id = $1 OR
-- email = $2), on the other hand, CANNOT USE the primary key: the two sides of
-- the OR want separate indexes, and since this query also looks for deleted
-- rows it goes outside the partial index. It scans too, and this is accepted
-- knowingly.
CREATE INDEX IF NOT EXISTS customer_email_idx
    ON customer (email)
    WHERE deleted_at IS NULL;

-- customer_group is a customer segment. The "customer_group_id" attribute in
-- pricing's rule context corresponds to the id here; the link is made NOT at
-- the database level but through the computation context.
CREATE TABLE IF NOT EXISTS customer_group (
    id         TEXT PRIMARY KEY,
    name       TEXT        NOT NULL,
    metadata   JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ,
    CONSTRAINT customer_group_name_check     CHECK (name <> ''),
    CONSTRAINT customer_group_name_len_check CHECK (length(name) <= 255)
);

-- A group name is unique among LIVE groups.
--
-- The WHERE condition is the counterpart of the soft delete: a deleted group's
-- name leaves the index's scope and becomes reusable. Without the condition,
-- every name used even once would be occupied forever — and since a group can
-- be deleted, this is a concrete constraint (see queries/customer_group.sql,
-- SoftDeleteCustomerGroup).
CREATE UNIQUE INDEX IF NOT EXISTS customer_group_name_uniq
    ON customer_group (name)
    WHERE deleted_at IS NULL;

-- customer_group_customer is the MANY-TO-MANY link between customer and group.
--
-- The composite primary key prevents the same customer from being added to the
-- same group twice; a membership is a set and carries no multiplicity.
CREATE TABLE IF NOT EXISTS customer_group_customer (
    customer_id       TEXT        NOT NULL REFERENCES customer(id) ON DELETE CASCADE,
    customer_group_id TEXT        NOT NULL REFERENCES customer_group(id) ON DELETE CASCADE,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (customer_id, customer_group_id)
);

-- Listing a group's members cannot use the primary key's PREFIX; the reverse
-- direction needs an index of its own.
CREATE INDEX IF NOT EXISTS customer_group_customer_group_idx
    ON customer_group_customer (customer_group_id);

-- customer_address is a customer's saved address.
--
-- The address is bound to customer by a foreign key: both are this module's
-- data, and an address record without an owner has no meaning.
CREATE TABLE IF NOT EXISTS customer_address (
    id                  TEXT        PRIMARY KEY,
    customer_id         TEXT        NOT NULL REFERENCES customer(id) ON DELETE CASCADE,
    first_name          TEXT        NOT NULL DEFAULT '',
    last_name           TEXT        NOT NULL DEFAULT '',
    company             TEXT        NOT NULL DEFAULT '',
    address_1           TEXT        NOT NULL,
    address_2           TEXT        NOT NULL DEFAULT '',
    city                TEXT        NOT NULL,
    country_code        TEXT        NOT NULL,
    postal_code         TEXT        NOT NULL DEFAULT '',
    phone               TEXT        NOT NULL DEFAULT '',
    is_default_shipping BOOLEAN     NOT NULL DEFAULT FALSE,
    is_default_billing  BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at          TIMESTAMPTZ,
    CONSTRAINT customer_address_address1_check CHECK (address_1 <> ''),
    CONSTRAINT customer_address_city_check     CHECK (city <> ''),
    CONSTRAINT customer_address_country_check  CHECK (country_code ~ '^[A-Z]{2}$')
);

CREATE INDEX IF NOT EXISTS customer_address_customer_idx
    ON customer_address (customer_id)
    WHERE deleted_at IS NULL;

-- The default shipping/billing address is ONE PER CUSTOMER, and the database
-- enforces it.
--
-- The rule could have been kept in the application too ("clear the old one
-- before writing the new one"), but it would not hold between two concurrent
-- requests: both would clear the old default, both would mark their own
-- address, and the customer would be left with two default shipping addresses.
-- The partial unique index makes this race impossible; the second write comes
-- back with a uniqueness violation. The clearing step in the application is
-- still needed, but it is no longer the source of CORRECTNESS, only the way to
-- satisfy the constraint.
CREATE UNIQUE INDEX IF NOT EXISTS customer_address_default_shipping_uniq
    ON customer_address (customer_id)
    WHERE is_default_shipping AND deleted_at IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS customer_address_default_billing_uniq
    ON customer_address (customer_id)
    WHERE is_default_billing AND deleted_at IS NULL;
