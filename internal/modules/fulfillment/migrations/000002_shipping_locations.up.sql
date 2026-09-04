-- Schema of the warehouse selection POLICY.
--
-- 000001 had closed with this limitation: the shipping module had no LOCATION
-- MODEL. The candidates came from the inventory module (a fact), the shipping
-- module made the selection (a decision) but it held no data at all to decide
-- with and the rule had stayed as "the candidate with the smallest id". The two
-- tables here bring that data.
--
-- Ownership: both tables belong ONLY to the fulfillment module.
-- shipping_locations.location_id is the inventory module's location id and is
-- NOT an FK (Principle 2.2 — the cross-module FK ban). Keeping a foreign id
-- opaque and FK-less is not new: shipping_options.region_id does the same. What
-- is new is that this id is the PRIMARY KEY; the rationale sits at the head of
-- the table.
--
-- The module DOES NOT COPY the ADDRESS or the NAME. Where the warehouse is, is
-- the inventory module's knowledge and stays there; what sits here is only the
-- warehouse's SHIPPING attribute: which regions it serves and in which order it
-- is preferred. A second name/address copy would mean two sources of truth in
-- two modules.

-- shipping_locations is the shipping policy of an inventory location.
--
-- # The ABSENCE of a row is an answer too
--
-- A warehouse without a policy row is a valid warehouse: it is at the default
-- priority (0) and serves ALL regions. That is why, while the table holds no
-- rows at all, selection is exactly the same as the behavior BEFORE this
-- migration — the tie is broken by the candidate with the smallest id. The
-- strict alternative (a warehouse without a policy cannot be a candidate) would
-- have stopped ALL orders of existing installations the day it was turned on.
--
-- # THE PRIMARY KEY IS A FOREIGN ID
--
-- The row's id is the id the inventory module produced, and this HAS NO
-- PRECEDENT in the repository: shipping_options.region_id is foreign and FK-less
-- too, but there the row has an id of its own (id) and region_id is an
-- ATTRIBUTE. What the two share is the opacity and the FK-lessness, not being
-- the key.
--
-- The new pattern is deliberate, because the row here has no independent
-- existence: a warehouse has AT MOST one policy, and a policy means nothing
-- without its warehouse. Giving it a separate id would produce a second id that
-- nothing needs and would make it possible to write two rows for the same
-- warehouse — preventing that would again require a uniqueness constraint on
-- location_id.
--
-- The price: the write path can produce a row for a warehouse that DOES NOT
-- exist; the module cannot validate the id because it does not know the
-- inventory module. Such a row can NEVER BE SELECTED (the policy only filters
-- and orders the candidates the inventory module produced, it cannot add an
-- element to the set) but it IS VISIBLE in the admin listing: because it carries
-- no name and no address, it stands on the screen as an opaque id that cannot be
-- resolved.
--
-- # DELETION IS NOT SOFT
--
-- Deletion in the module is soft as a rule (deleted_at); these two tables are
-- the third and fourth exceptions added to the list at the head of 000001. The
-- rationale is this: the row here IS NOT THE RECORD OF SOMETHING THAT HAPPENED,
-- it is a CONFIGURATION. The effect of a soft-deleted policy row is EXACTLY THE
-- SAME as that of a row that never existed (both mean "default"), so the
-- distinction would carry no meaning. Worse, it would be harmful: because
-- location_id is the primary key, a soft-deleted row would prevent a new policy
-- from being written for the same warehouse and every write path would have to
-- learn the "resurrect first" step.
--
-- # priority: THE SMALLER ONE WINS, negative IS ALLOWED
--
-- 0 is the default and is at the same rank as a warehouse that HAS NO policy
-- row. There is a concrete reason for permitting negatives: an operator who
-- wants to put one of three warehouses first must be able to write a single row
-- and give that warehouse -1. Had only non-negative values been permitted, the
-- same job would have required writing rows for the other two warehouses, which
-- the operator DOES NOT WANT to promote.
CREATE TABLE IF NOT EXISTS shipping_locations (
    location_id TEXT        PRIMARY KEY,
    priority    BIGINT      NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT shipping_locations_location_check CHECK (location_id <> '')
);

CREATE INDEX IF NOT EXISTS shipping_locations_priority_idx
    ON shipping_locations (priority, location_id);

-- shipping_location_regions are the shipping regions the warehouse SERVES.
--
-- region_id is the region module's id and is NOT an FK (Principle 2.2), just
-- like shipping_options.region_id. location_id, in turn, is an INTRA-MODULE FK
-- and is free; when the policy row is deleted its regions drop with it
-- (ON DELETE CASCADE), because an ownerless region bond cannot affect any
-- decision.
--
-- # A warehouse with NO bond serves ALL regions
--
-- The rule is the same as the rule for sales channel scope ("a product with no
-- channel assignment is visible in all channels") and it carries the same trap:
-- deleting a warehouse's LAST region bond does not close it, it opens it to ALL
-- regions. To close it, the warehouse is deleted in the inventory module or its
-- stock is zeroed — a shipping policy cannot make a warehouse exist or not
-- exist, it only filters and orders among the candidates.
--
-- The reason the trap is deliberate is the same as the state before the table
-- existed: counting the empty set as "serves no region" would render every
-- installation that has no policy today unable to take orders.
--
-- # THE TRAP IN THE REVERSE DIRECTION IS HEAVIER
--
-- The asymmetry with the sales channel rule is here: there a wrong scope HIDES
-- the product, here it DROPS the order. Binding a region id that does not exist
-- — or deleting a region and reopening it under the same name, because the new
-- record gets a new id — means that warehouse is eliminated in every cart; in a
-- single-warehouse installation the result is that every checkout is rejected
-- even though the catalog is full.
--
-- The bond is therefore NOT A PREFERENCE but a CONSTRAINT. "Prefer Istanbul but
-- ship from Ankara when it runs out" IS NOT WRITTEN with a region bond, it is
-- written with priority: neither is given a bond, Istanbul is given a small
-- priority. A region bond is only correct for "this warehouse cannot ship
-- there".
--
-- The fault is visible, but the visibility HAS A LIMIT: when the filtering
-- leaves nothing behind, the shipping module returns ITS OWN error code and that
-- code reaches all the way to the storefront. The dump that writes down which
-- regions the candidates are actually bound to, however, is in the SERVER LOG
-- and in the execution record — a dead id can only be diagnosed by an operator
-- who looks there.
CREATE TABLE IF NOT EXISTS shipping_location_regions (
    location_id TEXT        NOT NULL REFERENCES shipping_locations (location_id) ON DELETE CASCADE,
    region_id   TEXT        NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (location_id, region_id),
    CONSTRAINT shipping_location_regions_region_check CHECK (region_id <> '')
);

-- A read from region towards warehouse ("which warehouses serve this region")
-- DOES NOT EXIST TODAY and that direction WAS NOT INDEXED. The primary key
-- already indexes the (location_id, region_id) direction and every query reads
-- from that direction; an index with no user loads a cost onto every write and
-- tells the reader about a use that does not exist. It is added when needed.
