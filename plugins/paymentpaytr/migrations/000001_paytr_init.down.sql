-- Rolling the paytr schema back.
--
-- The index falls with the table; it is dropped explicitly anyway, because the
-- name space is shared with tables and the day it moves this file would
-- silently be incomplete.
--
-- THE DATA LOSS HERE IS REAL AND IS NOT RECOVERABLE FROM PAYTR. This table is
-- the only record on this side of what PayTR reported, and PayTR offers no
-- "list every payment" query to rebuild it from. Rolling back means the
-- installation can no longer answer "was this session paid" for any payment it
-- already took.
--
-- It is still a correct down migration — a reversible schema change is the
-- contract — but this is the one plugin whose rollback should be preceded by a
-- dump of its table.
DROP INDEX IF EXISTS paytr_payment_pending_idx;
DROP TABLE IF EXISTS paytr_payment;
