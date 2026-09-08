-- Schema of the inventory module (plan Phase 4).
--
-- Ownership: the four tables in this file belong to the inventory module ONLY.
-- Foreign keys inside the module are free and are used; no REFERENCES is given
-- to another module's table (Principle 2.2 — the cross-module FK ban).
-- That is why inventory_reservations.line_item_id (the cart module's row) and
-- the tie between inventory_items and a product variant are NOT FKs: the second
-- one is established through Module Links.
--
-- There is no currency; this module carries QUANTITIES only, and quantities are
-- BIGINT. Time: every stamp is timestamptz (UTC). Deletion is soft (deleted_at)
-- and every read query applies the deleted_at IS NULL filter.
--
-- THERE ARE TWO EXCEPTIONS and both were added after this file, so for those
-- two tables the CREATE TABLE below is HISTORY rather than the current schema:
--
--   * 000002 DROPS the deleted_at column of inventory_reservations. Nothing
--     ever wrote that column; a reservation is not deleted, its status changes
--     (the table comment below says so itself).
--   * 000003 REPLACES the deleted_at of stock_locations with closed_at. Nothing
--     ever wrote that one either, and writing it would not have been enough: a
--     hidden location goes on selling, because availability never joins this
--     table. A location is retired by CLOSING it, empty (ADR 0055).
--
-- The reasoning is at the head of each of those two files.

-- stock_locations is the place where stock physically sits (a warehouse, a shop).
CREATE TABLE IF NOT EXISTS stock_locations (
    id           TEXT        PRIMARY KEY,
    name         TEXT        NOT NULL,
    address_1    TEXT,
    address_2    TEXT,
    city         TEXT,
    province     TEXT,
    postal_code  TEXT,
    country_code TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at   TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS stock_locations_alive_idx
    ON stock_locations (created_at DESC)
    WHERE deleted_at IS NULL;

-- inventory_items is the item whose stock is tracked. Its tie to a product
-- variant is established through the "product_variant_inventory" link; this
-- module does not know product.
CREATE TABLE IF NOT EXISTS inventory_items (
    id                TEXT        PRIMARY KEY,
    sku               TEXT        NOT NULL,
    title             TEXT,
    description       TEXT,
    requires_shipping BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at        TIMESTAMPTZ
);

-- A SKU is unique only among LIVING items; the SKU of a deleted item can be
-- used again.
CREATE UNIQUE INDEX IF NOT EXISTS inventory_items_sku_uniq
    ON inventory_items (sku)
    WHERE deleted_at IS NULL;

-- inventory_levels is an item's stock position at one location.
--
-- available (the sellable quantity) is NOT STORED, it is DERIVED as
-- stocked_quantity - reserved_quantity. Storing the derived value would be a
-- source of inconsistency in which the two columns could drift apart from each
-- other; the constraint too is therefore built on the derivation.
CREATE TABLE IF NOT EXISTS inventory_levels (
    id                TEXT        PRIMARY KEY,
    inventory_item_id TEXT        NOT NULL REFERENCES inventory_items (id) ON DELETE CASCADE,
    location_id       TEXT        NOT NULL REFERENCES stock_locations (id) ON DELETE CASCADE,
    stocked_quantity  BIGINT      NOT NULL DEFAULT 0,
    reserved_quantity BIGINT      NOT NULL DEFAULT 0,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at        TIMESTAMPTZ,

    CONSTRAINT inventory_levels_stocked_nonneg  CHECK (stocked_quantity >= 0),
    CONSTRAINT inventory_levels_reserved_nonneg CHECK (reserved_quantity >= 0),
    -- The sellable quantity CANNOT fall negative. The service layer already
    -- rejects that; the constraint here is the last defence: an intervention
    -- made directly in SQL cannot drive the stock negative either.
    CONSTRAINT inventory_levels_available_nonneg CHECK (reserved_quantity <= stocked_quantity)
);

-- The (item, location) pair is unique among LIVING rows.
CREATE UNIQUE INDEX IF NOT EXISTS inventory_levels_item_location_uniq
    ON inventory_levels (inventory_item_id, location_id)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS inventory_levels_location_idx
    ON inventory_levels (location_id)
    WHERE deleted_at IS NULL;

-- inventory_reservations are the quantities set aside from sellable stock.
--
-- State machine: active -> released | confirmed. A record is NEVER DELETED; the
-- compensation (ReleaseReservation) being able to be idempotent depends on the
-- record's status staying readable — a deleted reservation and a reservation
-- that never existed could not be told apart from each other.
CREATE TABLE IF NOT EXISTS inventory_reservations (
    id                TEXT        PRIMARY KEY,
    inventory_item_id TEXT        NOT NULL REFERENCES inventory_items (id) ON DELETE CASCADE,
    location_id       TEXT        NOT NULL REFERENCES stock_locations (id) ON DELETE CASCADE,
    quantity          BIGINT      NOT NULL,
    -- line_item_id is the cart module's line id. THERE IS NO FK (Principle 2.2).
    line_item_id      TEXT,
    status            TEXT        NOT NULL DEFAULT 'active',
    description       TEXT,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at        TIMESTAMPTZ,

    CONSTRAINT inventory_reservations_quantity_positive CHECK (quantity > 0),
    CONSTRAINT inventory_reservations_status_valid
        CHECK (status IN ('active', 'released', 'confirmed'))
);

CREATE INDEX IF NOT EXISTS inventory_reservations_item_idx
    ON inventory_reservations (inventory_item_id, location_id)
    WHERE status = 'active' AND deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS inventory_reservations_line_item_idx
    ON inventory_reservations (line_item_id)
    WHERE line_item_id IS NOT NULL AND deleted_at IS NULL;
