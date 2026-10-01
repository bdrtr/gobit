-- A cart names the operator who opened it through the admin cart surface
-- (ADR 0296), so a telephone order left half built can be found again. A cart
-- a shopper opened names nobody. The value is the operator's identity as the
-- guard ring proved it; this module does not read the auth module's tables.
ALTER TABLE carts
    ADD COLUMN IF NOT EXISTS opened_by TEXT;
ALTER TABLE carts
    ADD CONSTRAINT carts_opened_by_not_blank CHECK (opened_by IS NULL OR length(btrim(opened_by)) > 0);

-- The operators' carts are a few among the storefront's, which nothing deletes,
-- so the listing that keeps them walks this index rather than every cart
-- (measurements/0296).
CREATE INDEX IF NOT EXISTS carts_opened_by_idx
    ON carts (created_at DESC, id DESC)
    WHERE opened_by IS NOT NULL AND deleted_at IS NULL;
