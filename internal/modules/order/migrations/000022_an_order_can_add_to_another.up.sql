-- An order can add to another order of the same customer (ADR 0192).
--
-- An addition is an ordinary order in every other respect: its own lines,
-- totals, payment, stock movements and invoice. What this column records is
-- the one fact the addition's cart carried in, the order the customer was
-- adding to, so the two are read together.
--
-- The reference stays inside this module, so it is a real foreign key
-- (Principle 2.2 is about identifiers of OTHER modules). Orders are never
-- deleted (000010), so the key needs no ON DELETE clause.
--
-- The rules that need the parent's row (it is pending, of the same customer and
-- currency, and not an addition itself) are checked in the transaction that
-- writes the addition, under a share lock on the parent, because a CHECK can
-- only read its own row. The one rule that needs no other row is here.
ALTER TABLE orders
    ADD COLUMN IF NOT EXISTS adds_to_order_id TEXT REFERENCES orders (id);

-- The IS NULL arm is not decoration: `adds_to_order_id <> id` alone answers
-- NULL for every order that adds to nothing, and a CHECK that answers NULL
-- passes the row (ADR 0169).
ALTER TABLE orders
    ADD CONSTRAINT orders_adds_to_another
        CHECK (adds_to_order_id IS NULL OR adds_to_order_id <> id);

-- The admin listing reads an order's additions. Most orders are not an
-- addition, and those rows are never an answer.
CREATE INDEX IF NOT EXISTS orders_adds_to_order_idx
    ON orders (adds_to_order_id, created_at DESC, id DESC)
    WHERE adds_to_order_id IS NOT NULL;
