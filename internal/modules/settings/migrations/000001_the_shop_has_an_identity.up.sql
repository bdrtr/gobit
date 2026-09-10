-- store_profile is who the shop IS, as a record rather than as a parameter.
--
-- # Why it is a row and not configuration in the binary
--
-- The shop's legal name, tax number, tax office and address change while the
-- shop runs: an office moves, a company is renamed, a number is corrected. A
-- value compiled into the embedding application needs a redeploy for each of
-- those, and an operator who can edit an order cannot edit the identity printed
-- on the invoice for that order.
--
-- # Why it is exactly ONE row
--
-- ADR 0009 puts multi-tenancy at the INSTALLATION boundary: one installation is
-- one shop. A table that could hold two profiles would be a second answer to
-- "who is selling", and every reader would then need a rule for choosing between
-- them. The primary key is fixed by a CHECK, which is the cheapest way for the
-- schema itself to say "one".
--
-- # Why it starts EMPTY
--
-- Nothing here can be guessed. A seeded row would put a placeholder name on a
-- real invoice, which is the one thing a document must never carry; an absent
-- profile is refused loudly by whoever needs it instead.
CREATE TABLE IF NOT EXISTS store_profile (
    id           TEXT        PRIMARY KEY,
    -- legal_name is the name the document is issued under; it is the one field
    -- with no sensible empty value.
    legal_name   TEXT        NOT NULL,
    -- tax_number is the VKN/TCKN or its equivalent, and tax_office the office it
    -- belongs to. Both may be empty: a shop that is not registered for tax has
    -- neither, and a country outside Turkey has no office.
    tax_number   TEXT        NOT NULL DEFAULT '',
    tax_office   TEXT        NOT NULL DEFAULT '',
    email        TEXT        NOT NULL DEFAULT '',
    -- address is the printed address, already formatted into lines by whoever
    -- typed it. It is one column and not six because it is PRINTED and never
    -- queried: splitting it would invent a structure no reader asks for.
    address      TEXT        NOT NULL DEFAULT '',
    country_code TEXT        NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT store_profile_singleton     CHECK (id = 'default'),
    CONSTRAINT store_profile_name_present  CHECK (legal_name <> ''),
    CONSTRAINT store_profile_country_valid CHECK (country_code ~ '^[A-Z]{2}$')
);
