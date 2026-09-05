-- Back to a relay that retries forever.
--
-- The rollback is lossy in one direction and it has to be said out loud: the
-- dead letters do not come back as pending rows, they come back as ordinary
-- unpublished rows, because that is what they were before this migration
-- existed. An installation that rolls back with dead letters in the table
-- resumes retrying them every minute — which is the behavior 000002 was
-- written to end, and the correct outcome of asking for it back.
--
-- The order is the up in reverse: the indexes, then the constraint, then the
-- columns. Dropping the columns first would take the constraint and the index
-- with them (PostgreSQL drops dependents of a dropped column silently), and the
-- explicit statements would then be no-ops that look like they did something.
DROP INDEX IF EXISTS event_outbox_dead_letter_idx;

DROP INDEX IF EXISTS event_outbox_due_idx;

-- 000001's index, restored exactly as it was written there. The up-after-down
-- round trip is what proves this line right, and the outbox is one of the core
-- owners the module migration gate does not walk (ADR 0018), so that proof is
-- an integration test in this package rather than an architecture test.
CREATE INDEX IF NOT EXISTS event_outbox_pending_idx
    ON event_outbox (created_at, id)
    WHERE published_at IS NULL;

ALTER TABLE event_outbox
    DROP CONSTRAINT IF EXISTS event_outbox_dead_letter_is_unpublished;

ALTER TABLE event_outbox
    DROP COLUMN IF EXISTS dead_lettered_at,
    DROP COLUMN IF EXISTS next_attempt_at;
