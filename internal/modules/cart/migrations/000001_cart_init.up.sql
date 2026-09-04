-- Schema of the cart module (plan Phase 5).
--
-- Ownership: the four tables here belong ONLY to the cart module. Foreign keys
-- INSIDE the module are free to use and are used (line items, addresses and
-- shipping methods hang off the cart with ON DELETE CASCADE); no REFERENCES IS
-- GIVEN to another module's table (Principle 2.2 — the cross-module FK ban).
-- That is why carts.region_id, carts.customer_id and cart_line_items.variant_id
-- are free TEXT: the relation is established through Module Links.
--
-- Money: ALL amounts are BIGINT and in the minor unit (cents); the currency
-- lives in a separate column (plan Section 8). Floating point is used nowhere.
--
-- Time: every stamp is timestamptz (UTC). Deletion is soft (deleted_at) and all
-- read queries filter deleted_at IS NULL.

-- carts is a shopping cart.
--
-- customer_id may be NULL: a guest cart belongs to a customer that has no
-- identity and is tracked by e-mail.
--
-- THE TOTAL FIELDS (subtotal, discount_total, tax_total, shipping_total, total)
-- are NOT COMPUTED by this module. The computation belongs to the
-- calculate_totals WORKFLOW, which takes the price from pricing and the tax
-- from tax/region (plan Section 2.5, ADR 0006); the module only STORES and
-- VALIDATES. The database-level counterpart of that validation is the
-- carts_totals_consistent constraint: when the identity breaks, the row is
-- never written at all. The service performs the same check first with a more
-- readable error; the constraint here is the last line of defense and it also
-- covers an intervention made directly through SQL.
--
-- revision / totals_revision MAKE A STALE TOTAL VISIBLE. Every operation that
-- changes the shape of the cart (adding/updating/deleting a line item, writing
-- an address, adding/removing a shipping method) increments revision by one;
-- calculate_totals, which writes the totals, stamps the revision of that moment
-- onto totals_revision. If the two are equal, the totals belong to the CURRENT
-- shape of the cart. The alternatives were bad: silently keeping a stale total
-- means showing the customer a wrong amount, and zeroing the totals means
-- saying "free". Here staleness is neither hidden nor invented — it is a
-- readable fact, and complete_cart in Phase 6 can reject a stale cart.
CREATE TABLE IF NOT EXISTS carts (
    id              TEXT        PRIMARY KEY,
    -- region_id is the region module's identity; there is NO FK (Principle 2.2).
    region_id       TEXT        NOT NULL,
    -- customer_id is the customer module's identity; NULL means a guest cart.
    customer_id     TEXT,
    email           TEXT,
    -- currency_code is the ISO 4217 code and is stored in UPPER case. The side
    -- that COPIES the value from the region is the workflow; the cart module
    -- does not call region.
    currency_code   TEXT        NOT NULL,
    subtotal        BIGINT      NOT NULL DEFAULT 0,
    discount_total  BIGINT      NOT NULL DEFAULT 0,
    tax_total       BIGINT      NOT NULL DEFAULT 0,
    shipping_total  BIGINT      NOT NULL DEFAULT 0,
    total           BIGINT      NOT NULL DEFAULT 0,
    -- revision is the cart's shape counter; it increases on every structural
    -- change that affects the totals.
    revision        BIGINT      NOT NULL DEFAULT 0,
    -- totals_revision stamps which shape the totals were computed for.
    totals_revision BIGINT      NOT NULL DEFAULT 0,
    metadata        JSONB       NOT NULL DEFAULT '{}'::jsonb,
    -- If completed_at is set the cart is IMMUTABLE; it is the record the order
    -- history rests on.
    completed_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at      TIMESTAMPTZ,

    CONSTRAINT carts_subtotal_nonneg       CHECK (subtotal >= 0),
    CONSTRAINT carts_discount_total_nonneg CHECK (discount_total >= 0),
    CONSTRAINT carts_tax_total_nonneg      CHECK (tax_total >= 0),
    CONSTRAINT carts_shipping_total_nonneg CHECK (shipping_total >= 0),
    CONSTRAINT carts_total_nonneg          CHECK (total >= 0),
    CONSTRAINT carts_totals_consistent
        CHECK (total = subtotal - discount_total + tax_total + shipping_total),
    CONSTRAINT carts_revision_nonneg       CHECK (revision >= 0),
    -- Totals cannot be stamped for a shape that DOES NOT EXIST YET.
    CONSTRAINT carts_totals_revision_range
        CHECK (totals_revision >= 0 AND totals_revision <= revision)
);

CREATE INDEX IF NOT EXISTS carts_alive_idx
    ON carts (created_at DESC, id DESC)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS carts_customer_idx
    ON carts (customer_id)
    WHERE customer_id IS NOT NULL AND deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS carts_region_idx
    ON carts (region_id)
    WHERE deleted_at IS NULL;

-- cart_line_items is a line in the cart.
--
-- variant_id is the product module's identity and IS NOT A FOREIGN KEY
-- (Principle 2.2): the cart keeps carrying its own history even if the catalog
-- is deleted. That is why title and unit_price are COPIED too; even if the
-- variant's name changes later, the name seen in the cart does not change.
CREATE TABLE IF NOT EXISTS cart_line_items (
    id             TEXT        PRIMARY KEY,
    cart_id        TEXT        NOT NULL REFERENCES carts (id) ON DELETE CASCADE,
    -- variant_id is the product module's identity; there is NO FK (Principle 2.2).
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

    CONSTRAINT cart_line_items_quantity_positive     CHECK (quantity > 0),
    CONSTRAINT cart_line_items_unit_price_nonneg     CHECK (unit_price >= 0),
    CONSTRAINT cart_line_items_subtotal_nonneg       CHECK (subtotal >= 0),
    CONSTRAINT cart_line_items_discount_total_nonneg CHECK (discount_total >= 0),
    CONSTRAINT cart_line_items_tax_total_nonneg      CHECK (tax_total >= 0),
    CONSTRAINT cart_line_items_total_nonneg          CHECK (total >= 0),
    CONSTRAINT cart_line_items_totals_consistent
        CHECK (total = subtotal - discount_total + tax_total)
);

-- A variant appears in AT MOST ONE line of a cart: when the same variant is
-- added a second time no new line is opened, the quantity of the existing line
-- increases (see service.AddLineItem). The constraint is the structural
-- counterpart of that decision — even if two concurrent additions slip past the
-- cart's row lock, a second line cannot be opened.
CREATE UNIQUE INDEX IF NOT EXISTS cart_line_items_cart_variant_uniq
    ON cart_line_items (cart_id, variant_id)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS cart_line_items_cart_idx
    ON cart_line_items (cart_id, created_at, id)
    WHERE deleted_at IS NULL;

-- cart_addresses is the cart's shipping/billing address.
--
-- The address is COPIED from the address in the customer module and the cart
-- keeps its own copy. When the customer later changes or deletes the record in
-- their address book, the past cart (and the order born out of it) is not
-- broken. source_address_id only documents the origin; IT IS NOT A FOREIGN KEY
-- and is not used for reads.
CREATE TABLE IF NOT EXISTS cart_addresses (
    id                TEXT        PRIMARY KEY,
    cart_id           TEXT        NOT NULL REFERENCES carts (id) ON DELETE CASCADE,
    -- address_type is either 'shipping' or 'billing'.
    address_type      TEXT        NOT NULL,
    -- source_address_id is the identity of the customer address it was copied
    -- from; there is NO FK.
    source_address_id TEXT,
    first_name        TEXT,
    last_name         TEXT,
    company           TEXT,
    address_1         TEXT,
    address_2         TEXT,
    city              TEXT,
    province          TEXT,
    postal_code       TEXT,
    country_code      TEXT,
    phone             TEXT,
    metadata          JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at        TIMESTAMPTZ,

    CONSTRAINT cart_addresses_type_valid
        CHECK (address_type IN ('shipping', 'billing'))
);

-- There is AT MOST ONE living address of each type per cart.
CREATE UNIQUE INDEX IF NOT EXISTS cart_addresses_cart_type_uniq
    ON cart_addresses (cart_id, address_type)
    WHERE deleted_at IS NULL;

-- cart_shipping_methods is the shipping method selected for the cart.
--
-- shipping_option_id is the fulfillment module's identity (Phase 7) and IS NOT
-- A FOREIGN KEY; in Phase 5 the option catalog does not exist yet, so it may be
-- NULL. amount is in the minor unit and is summed into the cart's
-- shipping_total by the workflow; this table does not write the total itself.
CREATE TABLE IF NOT EXISTS cart_shipping_methods (
    id                 TEXT        PRIMARY KEY,
    cart_id            TEXT        NOT NULL REFERENCES carts (id) ON DELETE CASCADE,
    name               TEXT        NOT NULL,
    -- shipping_option_id is the fulfillment module's identity; there is NO FK.
    shipping_option_id TEXT,
    amount             BIGINT      NOT NULL DEFAULT 0,
    data               JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at         TIMESTAMPTZ,

    CONSTRAINT cart_shipping_methods_amount_nonneg CHECK (amount >= 0)
);

-- The same shipping option cannot be added to a cart twice. Methods without an
-- option (NULL) are outside the constraint: NULLs are not counted as equal to
-- each other in a unique index, and until Phase 7 every method is optionless.
CREATE UNIQUE INDEX IF NOT EXISTS cart_shipping_methods_cart_option_uniq
    ON cart_shipping_methods (cart_id, shipping_option_id)
    WHERE shipping_option_id IS NOT NULL AND deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS cart_shipping_methods_cart_idx
    ON cart_shipping_methods (cart_id, created_at, id)
    WHERE deleted_at IS NULL;
