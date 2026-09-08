-- Dropping the columns drops the three constraints with them; naming the
-- constraints as well would fail on a database where the columns are already
-- gone, which is the state a repeated down-migration is in.
ALTER TABLE reviews
    DROP COLUMN suggestion_model,
    DROP COLUMN suggestion_note,
    DROP COLUMN suggested_at,
    DROP COLUMN suggested_status;
