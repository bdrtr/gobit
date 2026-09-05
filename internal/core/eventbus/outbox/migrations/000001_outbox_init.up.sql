-- The outbox: one row per event that a business transaction PROMISED.
--
-- # The window it closes
--
-- A module commits its work and then publishes. Between those two moments the
-- process can die, and then the work exists while the event never happened —
-- no confirmation mail is sent and nothing anywhere records that one is owed.
-- The event bus is honest about its own guarantee (in-memory loses events, Redis
-- resumes) but neither guarantee covers that window, because the publish is not
-- part of the transaction.
--
-- A row here IS part of it. The event is written with the business rows, in the
-- same transaction, and a relay sends it afterwards. If the process dies the row
-- is still there.
--
-- # Why published_at rather than a status
--
-- There are exactly two states and the timestamp carries both, plus WHEN. A
-- status column would need the timestamp anyway and could then disagree with it.
CREATE TABLE IF NOT EXISTS event_outbox (
    id           text        PRIMARY KEY,
    -- name is the event name in "module.action" form.
    name         text        NOT NULL,
    -- data is the payload, stored as it will be published.
    data         jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at   timestamptz NOT NULL DEFAULT now(),
    -- published_at is NULL until the relay hands the event to the bus.
    published_at timestamptz,
    -- attempts counts how many times the relay tried.
    --
    -- It is kept so a permanently failing event is VISIBLE rather than merely
    -- slow: a row with a high count and no published_at is one a human has to
    -- look at, and without the count it looks identical to a row written a
    -- second ago.
    attempts     bigint      NOT NULL DEFAULT 0,
    -- last_error is why the last attempt failed; empty when it has not.
    last_error   text        NOT NULL DEFAULT '',

    CONSTRAINT event_outbox_name_not_empty CHECK (name <> ''),
    CONSTRAINT event_outbox_attempts_nonneg CHECK (attempts >= 0)
);

-- The relay's only query: the unpublished rows, oldest first.
--
-- It is PARTIAL on published_at, and that is what keeps the relay's cost flat
-- as the table grows: a published row leaves the index for good, so the index
-- holds the backlog rather than the history.
CREATE INDEX IF NOT EXISTS event_outbox_pending_idx
    ON event_outbox (created_at, id)
    WHERE published_at IS NULL;
