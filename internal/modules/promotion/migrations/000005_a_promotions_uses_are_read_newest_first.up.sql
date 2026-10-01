-- A promotion's uses are read newest first (ADR 0313).
--
-- The index on promotion_id alone leaves the order to the primary key, and
-- for a coupon whose uses are all older than the others' the planner walks
-- the whole table backwards to find its latest twenty. Ordering the index by
-- id under the promotion serves the equality and both directions, so it
-- replaces the 000001 index rather than sitting beside it.
CREATE INDEX IF NOT EXISTS promotion_redemption_promotion_id_idx
    ON promotion_redemption (promotion_id, id);

DROP INDEX IF EXISTS promotion_redemption_promotion_idx;
