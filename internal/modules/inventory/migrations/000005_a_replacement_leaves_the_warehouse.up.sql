-- A promise can say WHY it was made, and the ledger can say that units left as
-- a replacement rather than as a sale.
--
-- # What forced the column
--
-- Goods sent to settle a claim leave the warehouse exactly as a sale's do: they
-- are set aside, a parcel is opened, and the confirm takes them out of the
-- physical count. The arithmetic is identical and the FACT is not — nobody paid
-- for these units — and 000004 already decided how this repository treats that
-- difference: "the service has an entry point per reason, and a positive delta
-- says nothing about which one it was".
--
-- The reason cannot be a parameter of the confirm. The confirm is reached from
-- a saga, from a retry and from the recovery path, and a reason a caller passes
-- is a reason a caller can get wrong on the third of those. It belongs to the
-- promise, which is written once, by the flow that knows what it is for.
--
-- # Why the equivalence widens rather than loosens
--
-- 000004 wrote "(reason = 'sale') = (reservation_id IS NOT NULL)" and its
-- content is that units leaving against a promise NAME the promise. A
-- replacement leaves against one too, so the left side becomes a set. What is
-- NOT done is dropping the equivalence to an implication: a movement with no
-- reservation still cannot be a sale, and a stock count still cannot carry one.
ALTER TABLE inventory_reservations
    ADD COLUMN IF NOT EXISTS purpose TEXT NOT NULL DEFAULT 'sale';

-- The default is 'sale' and it is the honest value for every row that existed
-- before this column: each was written by the checkout saga, which is the only
-- caller the module had.
ALTER TABLE inventory_reservations
    DROP CONSTRAINT IF EXISTS inventory_reservations_purpose_valid;

ALTER TABLE inventory_reservations
    ADD CONSTRAINT inventory_reservations_purpose_valid
        CHECK (purpose IN ('sale', 'replacement'));

ALTER TABLE inventory_movements
    DROP CONSTRAINT IF EXISTS inventory_movements_reason_valid;

ALTER TABLE inventory_movements
    ADD CONSTRAINT inventory_movements_reason_valid
        CHECK (reason IN ('stock_count', 'adjustment', 'sale', 'return_restock', 'replacement'));

-- The sign is part of what the reason MEANS, and a replacement only ever takes
-- units out: goods coming back are a return, and this reason is the going-out
-- half.
ALTER TABLE inventory_movements
    ADD CONSTRAINT inventory_movements_replacement_deducts
        CHECK (reason <> 'replacement' OR delta < 0);

ALTER TABLE inventory_movements
    DROP CONSTRAINT IF EXISTS inventory_movements_sale_names_its_reservation;

ALTER TABLE inventory_movements
    ADD CONSTRAINT inventory_movements_promise_names_its_reservation
        CHECK ((reason IN ('sale', 'replacement')) = (reservation_id IS NOT NULL));
