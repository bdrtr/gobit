-- Retry with a ceiling, and a dead letter for what the ceiling stops.
--
-- # The two failure modes this schema exists to prevent
--
-- Before it, a publish failure did exactly one thing: it incremented `attempts`
-- and left the row pending. The next pass — a minute later — tried it again,
-- and so did every pass after that, forever. That produced BOTH of the failure
-- modes an outbox is supposed to be immune to, at once:
--
--   1. AN UNBOUNDED RETRY. A row whose payload the receiver will never accept
--      was re-sent every sixty seconds for as long as the installation lived.
--   2. A SILENT STOP. Not a dropped row — the row stayed — but a delivery that
--      stopped happening with nothing saying so. Measured on 2026-09-06 against
--      a real PostgreSQL with the relay's own code: the pass reads the OLDEST
--      pending rows up to its limit, so a number of permanently failing rows
--      equal to that limit fills every batch. Five consecutive passes published
--      NOTHING, and a healthy event written after them finished with
--      `attempts = 0` — it was never once attempted. The backlog does not
--      degrade delivery; it ENDS it.
--
-- `next_attempt_at` answers the first: a failed row is not eligible again until
-- its delay has passed, so a failing row stops consuming a slot in every batch.
-- `dead_lettered_at` answers the second: after the ceiling the row leaves the
-- relay's index entirely, which is what lets the events behind it move.
--
-- # Why two timestamps rather than a status column
--
-- The same argument 000001 made for `published_at`, applied twice. Each column
-- has two states and the timestamp carries both plus WHEN — when the row is
-- next due, and when it was given up on. A status enum would need both
-- timestamps anyway and could then disagree with them.
--
-- `dead_lettered_at` is a timestamp rather than the predicate
-- `attempts >= <ceiling>` for a second reason: the ceiling is a Go-side policy
-- an embedder may change. Raising it must not silently resurrect rows a human
-- was already told about, and lowering it must not retroactively kill rows
-- nobody was told about. The decision is recorded when it is made.
ALTER TABLE event_outbox
    -- next_attempt_at is the instant the relay may try this row again.
    --
    -- It defaults to now(), so a row is due the moment it is written: the
    -- FIRST attempt is never delayed, only the retries. Existing rows take the
    -- default too, which is the intended reading — everything already in the
    -- table is owed a delivery now.
    ADD COLUMN IF NOT EXISTS next_attempt_at timestamptz NOT NULL DEFAULT now(),
    -- dead_lettered_at is when the relay stopped trying; NULL while it has not.
    --
    -- Nullable and with no default on purpose: the absence of a value is the
    -- normal state, and a default here would mean every new row is born dead.
    ADD COLUMN IF NOT EXISTS dead_lettered_at timestamptz;

-- A row is given up on or delivered, never both.
--
-- The relay's own query already excludes dead rows, so this constraint is not
-- what enforces the flow — it is what catches a FUTURE writer that closes a row
-- both ways and leaves the two timestamps telling different stories. A dead
-- letter that also carries a publication instant would be unreadable: nobody
-- could say whether the event went out.
ALTER TABLE event_outbox
    ADD CONSTRAINT event_outbox_dead_letter_is_unpublished
        CHECK (dead_lettered_at IS NULL OR published_at IS NULL);

-- The relay's query changed, so its index has to.
--
-- 000001's index covered `published_at IS NULL`. That predicate now admits two
-- kinds of row the relay will never select: one whose backoff has not elapsed,
-- and one that has been given up on. The second is the one that matters — dead
-- rows accumulate and never leave — so the partial predicate gains
-- `dead_lettered_at IS NULL` and the dead letters drop out of the relay's index
-- for good, exactly as published rows already do.
--
-- All three sentences below are MEASURED, on PostgreSQL 16 with 40,000
-- published rows, 2,000 dead letters and 300 pending rows, reading the relay's
-- own select with EXPLAIN (ANALYZE, BUFFERS) and a LIMIT of 200:
--
--   * this index                       0.18 ms, 411 buffers, 0 rows filtered
--   * 000001's predicate, same key     1.38 ms, 840 buffers, 1,900 rows filtered
--   * key (next_attempt_at, created_at, id)
--                                      0.31 ms, and a Sort node
--
-- The middle line is the one that matters, and the number that matters in it is
-- not the millisecond but the 1,900: those are the dead rows the scan walks
-- past to find 200 live ones. They never leave 000001's index, so that figure
-- grows for as long as the installation runs — which is the difference between
-- a slower query and a query that gets slower.
--
-- The KEY stays (created_at, id) rather than leading with next_attempt_at
-- because the ORDER BY is on created_at: leading with the due instant makes the
-- planner sort. It would only pay if most pending rows were waiting out a
-- backoff, which is the outage case rather than the normal one.
DROP INDEX IF EXISTS event_outbox_pending_idx;

CREATE INDEX IF NOT EXISTS event_outbox_due_idx
    ON event_outbox (created_at, id)
    WHERE published_at IS NULL AND dead_lettered_at IS NULL;

-- The dead letters' own index, and it is not an optimization.
--
-- It is what makes the pile READABLE at the cost of the pile's size rather than
-- the table's. The relay asks "is there anything a human has to look at?" on
-- every pass, once a minute, forever; without this index that question is a
-- sequential scan of the whole history, and a question that expensive is one
-- somebody eventually stops asking. A ledger nobody reads is the mistake this
-- repository has already made once, in audit_log.
CREATE INDEX IF NOT EXISTS event_outbox_dead_letter_idx
    ON event_outbox (dead_lettered_at, id)
    WHERE dead_lettered_at IS NOT NULL;
