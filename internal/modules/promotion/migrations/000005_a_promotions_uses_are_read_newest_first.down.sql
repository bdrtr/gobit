-- Restores the 000001 index, on promotion_id alone, exactly as it was written.
CREATE INDEX IF NOT EXISTS promotion_redemption_promotion_idx
    ON promotion_redemption (promotion_id);

DROP INDEX IF EXISTS promotion_redemption_promotion_id_idx;
