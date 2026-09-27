-- A product carries typed attributes the storefront filters and counts on
-- (ADR 0219).
--
-- product_attribute is a store-wide definition: a handle the storefront names it
-- by, a title, a kind -- a number, a yes or no, or a choice among options -- and
-- the operator's order. product_attribute_option is a choice of a select
-- attribute. product_attribute_value is a product's value: one row per chosen
-- option, or one row holding the number or the boolean.
--
-- # Why the value row carries its attribute beside its option
--
-- A select value names an option, and the option names its attribute. The
-- composite foreign key makes the two agree, so a value row cannot claim one
-- attribute and point at another's option; the kind of the attribute is the
-- service's to match, since a check across tables is not a constraint.
--
-- # Soft deletes
--
-- A definition and an option are soft deleted like the rest of the catalog's
-- vocabulary; every read joins them on deleted_at IS NULL, so a value pointing
-- at a removed one names nothing. The value rows go with their product.
CREATE TABLE IF NOT EXISTS product_attribute (
    id         text        PRIMARY KEY,
    handle     text        NOT NULL,
    title      text        NOT NULL,
    kind       text        NOT NULL,
    rank       integer     NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,

    CONSTRAINT product_attribute_kind_check CHECK (kind IN ('number', 'boolean', 'select')),
    CONSTRAINT product_attribute_handle_check CHECK (handle ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    CONSTRAINT product_attribute_title_check CHECK (title <> '')
);

CREATE UNIQUE INDEX IF NOT EXISTS product_attribute_handle_uniq
    ON product_attribute (handle) WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS product_attribute_option (
    id           text        PRIMARY KEY,
    attribute_id text        NOT NULL REFERENCES product_attribute (id),
    handle       text        NOT NULL,
    value        text        NOT NULL,
    rank         integer     NOT NULL DEFAULT 0,
    created_at   timestamptz NOT NULL DEFAULT now(),
    deleted_at   timestamptz,

    CONSTRAINT product_attribute_option_handle_check CHECK (handle ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    CONSTRAINT product_attribute_option_value_check CHECK (value <> ''),
    CONSTRAINT product_attribute_option_attribute_uniq UNIQUE (id, attribute_id)
);

CREATE UNIQUE INDEX IF NOT EXISTS product_attribute_option_handle_uniq
    ON product_attribute_option (attribute_id, handle) WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS product_attribute_value (
    product_id    text             NOT NULL REFERENCES product (id) ON DELETE CASCADE,
    attribute_id  text             NOT NULL REFERENCES product_attribute (id),
    option_id     text,
    number_value  double precision,
    boolean_value boolean,

    CONSTRAINT product_attribute_value_option_fk
        FOREIGN KEY (option_id, attribute_id) REFERENCES product_attribute_option (id, attribute_id),
    CONSTRAINT product_attribute_value_one
        CHECK (num_nonnulls(option_id, number_value, boolean_value) = 1),
    CONSTRAINT product_attribute_value_finite
        CHECK (number_value IS NULL OR number_value NOT IN ('Infinity', '-Infinity', 'NaN'))
);

-- A product chooses an option once, and holds one number or one boolean per
-- attribute.
CREATE UNIQUE INDEX IF NOT EXISTS product_attribute_value_option_uniq
    ON product_attribute_value (product_id, option_id) WHERE option_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS product_attribute_value_scalar_uniq
    ON product_attribute_value (product_id, attribute_id) WHERE option_id IS NULL;

-- The storefront's filters read from the value to the product.
CREATE INDEX IF NOT EXISTS product_attribute_value_option_idx
    ON product_attribute_value (option_id) WHERE option_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS product_attribute_value_number_idx
    ON product_attribute_value (attribute_id, number_value) WHERE number_value IS NOT NULL;
CREATE INDEX IF NOT EXISTS product_attribute_value_boolean_idx
    ON product_attribute_value (attribute_id, boolean_value) WHERE boolean_value IS NOT NULL;
