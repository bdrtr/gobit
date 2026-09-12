-- The funnel's raw material: one row per EVENT, never a counter.
--
-- # Why rows and not counters
--
-- The bus delivers AT LEAST ONCE and promises no ordering. A counter
-- incremented per event is therefore wrong by construction — one redelivery and
-- the ratio is a number that never happened, with nothing in the data able to
-- say so afterwards.
--
-- The row's key is the EVENT's own id, which the publishing modules DERIVE from
-- the record ("cart.created:cart_01H…"), so the same event arriving twice is the
-- same primary key twice and the second insert does nothing. Idempotency is a
-- property of the table rather than an argument about arithmetic.
--
-- # What it does NOT store
--
-- No amount and no identity. The events carry neither (ADR 0153), and a funnel
-- does not need them: the question is how many, per day, per region.
CREATE TABLE IF NOT EXISTS analytics_events (
    -- id is the event's own identifier, DERIVED by the publisher. An event that
    -- arrives without one is refused by the plugin rather than written: an empty
    -- key would make the first such event the last one ever recorded.
    id          TEXT        PRIMARY KEY,

    -- topic is which of the three moments this row is. The vocabulary is closed
    -- by a CHECK because a misspelled topic would sum correctly into a column
    -- nobody reads and leave the funnel silently short.
    topic       TEXT        NOT NULL,

    -- occurred_at is the moment the PUBLISHER stamped, not the moment this row
    -- was written. The difference matters: the outbox relay can deliver a
    -- minute late, and a row dated by its own insert would move a shop's
    -- Tuesday into Wednesday.
    occurred_at TIMESTAMPTZ NOT NULL,

    -- day is occurred_at's date in UTC, stored rather than computed on read.
    --
    -- It is stored because the funnel groups by it and a GROUP BY over an
    -- expression cannot use an index on the column. UTC and not the shop's zone:
    -- the plugin does not know the shop's zone, and inventing one here would
    -- make the same event land on different days in two installations.
    day         DATE        NOT NULL,

    -- region_id is the shop's own dimension, carried in every one of the three
    -- payloads. It is NOT a foreign key (Principle 2.2): the region belongs to
    -- the region module and this plugin may not depend on its schema.
    region_id   TEXT        NOT NULL,

    CONSTRAINT analytics_events_topic_valid
        CHECK (topic IN ('cart.created', 'cart.completed', 'order.placed')),
    CONSTRAINT analytics_events_region_not_blank
        CHECK (length(btrim(region_id)) > 0)
);

-- The funnel's only access shape: a date window, grouped by day and region.
CREATE INDEX IF NOT EXISTS analytics_events_day_region_idx
    ON analytics_events (day, region_id, topic);
