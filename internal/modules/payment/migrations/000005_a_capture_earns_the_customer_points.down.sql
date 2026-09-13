-- Rolling back takes the points ledger with it.
--
-- The balance is SUM(points) and it is stored NOWHERE ELSE, so this drop sets
-- every customer's balance to zero with no way to recompute it: the capture
-- history says what was captured, not the rate it was earned at, and the rate is
-- an installation setting that may have changed since. The loss is smaller than
-- 000004's — points are not money the shop owes — but it is not nothing.
DROP TABLE IF EXISTS payment_loyalty_entries;
