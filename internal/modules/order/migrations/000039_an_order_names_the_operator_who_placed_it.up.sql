-- An order names the operator who placed it through the admin cart surface or
-- the panel's telephone order (ADR 0298); a shopper's order names nobody. The
-- value is the operator's identity as the guard ring proved it, free text as
-- the cart's opened_by is: this module does not read the auth module's tables.
ALTER TABLE orders
    ADD COLUMN IF NOT EXISTS placed_by TEXT;
ALTER TABLE orders
    ADD CONSTRAINT orders_placed_by_not_blank CHECK (placed_by IS NULL OR length(btrim(placed_by)) > 0);

-- The operators' orders are a few among the storefront's, so the listing that
-- keeps them walks this index rather than every order (measurements/0296 for
-- the same shape on carts).
CREATE INDEX IF NOT EXISTS orders_placed_by_idx
    ON orders (created_at DESC, id DESC)
    WHERE placed_by IS NOT NULL;
