-- inventory_movements: the ledger that EXPLAINS stocked_quantity (ADR 0068).
--
-- # What a row is, and what it is not
--
-- One row per change to the PHYSICAL count of one item at one location. The
-- table's meaning is deliberately single: it explains inventory_levels
-- .stocked_quantity and nothing else.
--
-- A RESERVATION IS NOT A MOVEMENT. Reserving and releasing move
-- reserved_quantity, so the goods have not gone anywhere — what changed is what
-- is AVAILABLE, and inventory_reservations is already that record, with its own
-- lifecycle and its own rule that it is never deleted (000002). Only the
-- CONFIRM leaves a row here, because only the confirm takes units out of the
-- physical count.
--
-- The other side of the same rule: a soft-deleted level takes its stock out of
-- every availability sum and writes NO movement, because the column this table
-- explains did not change. The units are still there; the row is hidden.
--
-- # stocked_quantity does NOT become derived, and this is what holds the two
--
-- The column stays authoritative. Deriving it would put an aggregate over this
-- table in front of every availability read, on the path the storefront listing
-- takes (AvailableQuantityByItemIDs), and the cost of an aggregate on a read
-- path is measured in this repository rather than guessed.
--
-- The price of keeping both is that they can drift, so two things hold them
-- together. The row is written in the SAME TRANSACTION as the column — the
-- repository's AppendMovement refuses to run outside one, exactly as the Lock
-- methods do — and every row carries stocked_after, the resulting count. A
-- drift is therefore visible from ONE row rather than by summing a history.
--
-- # What this says about the stock that was here before it
--
-- Nothing, and it must not pretend otherwise: a migration cannot invent
-- history. No opening balance is written, so the deltas of this table DO NOT
-- SUM to the current count. stocked_after is what makes that survivable — the
-- first movement of an item names the balance the ledger inherited, as
-- stocked_after - delta.
--
-- # There is no actor column
--
-- reason IS the answer to "who", one step better than a name nobody issued
-- (ADR 0056). Two of the four reasons come from an admin request, which
-- audit_log already records with the caller; the other two come from a flow
-- with no person behind it — a checkout confirming its reservation, a return
-- being received. A nullable actor would be empty on half the table and
-- unprovable on the rest, and audit_log is where the caller lives. What
-- audit_log cannot hold is the delta and the item; that is this table.
CREATE TABLE IF NOT EXISTS inventory_movements (
    id                TEXT        PRIMARY KEY,
    inventory_item_id TEXT        NOT NULL REFERENCES inventory_items (id) ON DELETE CASCADE,
    location_id       TEXT        NOT NULL REFERENCES stock_locations (id) ON DELETE CASCADE,
    -- reservation_id names the promise the units left against; it is set on a
    -- 'sale' row and on no other, which the CHECK below states as an
    -- equivalence rather than as an option. The gaps ledger's B7 row says
    -- audit_log "never sees a reservation the checkout saga takes"; this is the
    -- column that does.
    reservation_id    TEXT        REFERENCES inventory_reservations (id) ON DELETE CASCADE,
    reason            TEXT        NOT NULL,
    -- delta is the signed change and stocked_after the count it produced.
    delta             BIGINT      NOT NULL,
    stocked_after     BIGINT      NOT NULL,
    -- created_at comes from the DATABASE clock, and here that is not the
    -- default choice being taken quietly. The row is written inside the
    -- transaction that writes the level, and now() is transaction START, so the
    -- movement and the updated_at of the level it explains carry the SAME
    -- moment. A process clock would give the pair two moments a millisecond
    -- apart and invite a reader to wonder which is true (ADR 0053).
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- A movement that moves nothing is not a movement; it would be a row per
    -- no-op write, and the ledger would stop being readable.
    CONSTRAINT inventory_movements_delta_nonzero CHECK (delta <> 0),
    CONSTRAINT inventory_movements_after_nonneg CHECK (stocked_after >= 0),
    CONSTRAINT inventory_movements_reason_valid
        CHECK (reason IN ('stock_count', 'adjustment', 'sale', 'return_restock')),
    -- The sign is part of what the reason MEANS: a sale takes units out and a
    -- received return puts them back. The two operator reasons carry either
    -- sign, because a count and a correction go both ways.
    CONSTRAINT inventory_movements_sale_deducts
        CHECK (reason <> 'sale' OR delta < 0),
    CONSTRAINT inventory_movements_restock_adds
        CHECK (reason <> 'return_restock' OR delta > 0),
    CONSTRAINT inventory_movements_sale_names_its_reservation
        CHECK ((reason = 'sale') = (reservation_id IS NOT NULL))
);

-- The operator's question is "what happened to this item", newest first, and
-- this index is that question: the item narrows, and (created_at, id) is the
-- keyset the listing pages on, in the listing's own order. The id is the
-- tiebreaker because created_at alone is not unique — two movements committed
-- in the same transaction share it exactly — and a page boundary between two
-- such rows would drop one or repeat it.
--
-- There is NO second index on location_id. The listing's location filter runs
-- INSIDE one item's rows, which this index has already narrowed to the few
-- levels an item has; a second B-tree on the module's hottest write path would
-- be paid on every sale to save a filter over a handful of rows.
CREATE INDEX IF NOT EXISTS inventory_movements_item_idx
    ON inventory_movements (inventory_item_id, created_at DESC, id DESC);

-- There is no deleted_at, and the omission is the same one 000002 argued for
-- inventory_reservations: a movement is a record of something that HAPPENED.
-- A ledger whose rows can be hidden is not a ledger, and nothing in this module
-- deletes one — the row goes only when the item or the location it names does,
-- through the CASCADEs above.
