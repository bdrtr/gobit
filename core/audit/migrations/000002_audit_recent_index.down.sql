-- Rolling back the recent-activity index.
--
-- Dropping it loses no data: an index is derived, and the unfiltered listing
-- still answers — it simply falls back to scanning and sorting the whole log.
DROP INDEX IF EXISTS audit_log_created_at_idx;
