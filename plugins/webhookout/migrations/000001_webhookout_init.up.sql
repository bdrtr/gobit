-- The outbound webhook plugin's schema: a REGISTRY of receivers and a QUEUE of
-- deliveries owed to them.
--
-- The schema belongs to the PLUGIN and so does its version ledger
-- (webhookout_schema_migrations, see core/db.MigrationsTable): removing the
-- plugin leaves only these two tables behind and touches no module's ledger.
--
-- # Why the delivery queue is not the outbox
--
-- The obvious saving is to reuse event_outbox: it already has attempts,
-- next_attempt_at, dead_lettered_at, a backoff ladder and a dead-letter reader.
-- It was measured against a real PostgreSQL before this table was written, and
-- the schema itself is the refusal:
--
--   event_outbox is keyed on the EVENT (id text PRIMARY KEY) and carries ONE
--   attempts, ONE next_attempt_at and ONE dead_lettered_at. There is no
--   destination column, and the relay closes the row with
--   `UPDATE event_outbox SET published_at = now() WHERE id = $1` after a single
--   publish callback returns nil.
--
-- One event owed to three receivers, one of them down, therefore has no
-- expressible state in that row: marking it published loses the failure, and
-- leaving it pending re-delivers to the two receivers that already answered
-- 200. A fan-out needs one attempt counter PER DESTINATION, which is one row
-- per (endpoint, event) — this table.
--
-- The second reason is the alarm. The outbox relay job FAILS its run whenever
-- the dead-letter pile is non-empty, and that failure is what reaches
-- `gobit jobs`. Putting webhook deliveries in the same pile makes a third
-- party's decommissioned endpoint indistinguishable from gobit's own bus
-- failing to accept an event, in the one listing an operator reads to tell
-- those apart.

-- The receivers. A row here is the ONLY thing that causes an outbound request:
-- with an empty table the plugin subscribes, receives events and enqueues
-- nothing.
CREATE TABLE IF NOT EXISTS webhook_endpoint (
    -- id is the row's identity (whe_...). It is what the admin surface deletes
    -- by and what a delivery row records as its destination.
    id text PRIMARY KEY,

    -- url is where the delivery is POSTed. It is UNIQUE together with nothing:
    -- registering the same URL twice with different topic sets is legitimate
    -- (two teams, two secrets, one gateway), and a uniqueness constraint here
    -- would refuse it for tidiness rather than for correctness.
    url text NOT NULL,

    -- secret is the HMAC key the delivery signature is computed with.
    --
    -- It is stored RECOVERABLE, not hashed, and there is no alternative: the
    -- sender has to compute a MAC with it on every attempt. That puts it in the
    -- same class as the webpush VAPID key (ADR 0018) — durable secret state on
    -- the order of the database — and it is why the admin listing never returns
    -- it and the create response returns it exactly once.
    secret text NOT NULL,

    -- topics are the event names this receiver asked for.
    --
    -- An array rather than a child table because the whole domain is four
    -- names: a join table would buy set operations nothing here performs, and
    -- the containment test the enqueue path runs is one operator on this
    -- column. A topic the installation does not publish is refused at
    -- REGISTRATION, so this column cannot hold a name that will never fire.
    topics text[] NOT NULL,

    -- description is what a human wrote about the receiver. It is the only
    -- thing in the listing that answers "whose endpoint is this" six months
    -- later, when the URL is a load balancer name.
    description text NOT NULL DEFAULT '',

    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT webhook_endpoint_url_not_empty CHECK (url <> ''),
    CONSTRAINT webhook_endpoint_secret_not_empty CHECK (secret <> ''),
    -- A receiver with no topics is registered, visible, and can never be
    -- delivered to. That is the shape of defect this repository names most
    -- often, so the database refuses it rather than the API alone.
    CONSTRAINT webhook_endpoint_topics_not_empty CHECK (cardinality(topics) > 0)
);

-- One row per (receiver, event): the delivery that is owed.
CREATE TABLE IF NOT EXISTS webhook_delivery (
    -- id is the row's identity (whd_...) and it is also the value the receiver
    -- is told to deduplicate on: it travels in the X-Gobit-Delivery header, is
    -- covered by the signature, and is STABLE across retries.
    id text PRIMARY KEY,

    -- endpoint_id is the receiver this was owed to. There is deliberately NO
    -- foreign key.
    --
    -- ON DELETE CASCADE would be the natural choice and it is the wrong one
    -- here: deleting a receiver would erase the record of what was never
    -- delivered to it, which is the one question an operator asks after
    -- removing an integration. The row is kept and the url below is what makes
    -- it still readable.
    endpoint_id text NOT NULL,

    -- url is a SNAPSHOT of where this was sent, taken at enqueue time.
    --
    -- Not a join: the endpoint row can be edited or deleted, and a dead letter
    -- that reports today's URL for an attempt made against yesterday's is a
    -- record that quietly lies.
    url text NOT NULL,

    -- event_id is the bus event's own id. Together with endpoint_id it is
    -- UNIQUE, and that uniqueness is what makes the subscriber idempotent: the
    -- bus delivers at least once, and the second delivery of the same event
    -- must not enqueue a second POST.
    event_id text NOT NULL,

    -- event_name is the topic. It is stored rather than derived because the
    -- dead-letter listing has to say WHICH event was lost without reading the
    -- payload.
    event_name text NOT NULL,

    -- occurred_at is when the event happened, not when this row was written.
    -- The two differ by the outbox relay's interval when the direct publish was
    -- the one that failed, and a receiver that reconciles by time needs the
    -- former.
    occurred_at timestamptz NOT NULL,

    -- payload is the event's data as it will be sent, ALREADY REDACTED.
    --
    -- The redaction happens before the write, not before the send, and that is
    -- deliberate: a field that never enters this table cannot leak from it
    -- either, and the queue is readable by everyone who can read the database.
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,

    -- redacted names the fields that were removed. It is carried into the body
    -- so the receiver sees that something was WITHHELD rather than absent —
    -- the same distinction `gobit deadletters` makes about an outbox payload.
    redacted text[] NOT NULL DEFAULT '{}'::text[],

    -- attempts counts the POSTs that did not succeed.
    attempts bigint NOT NULL DEFAULT 0,

    -- next_attempt_at is the instant this row may be tried again. It defaults
    -- to now(), so the first attempt is never delayed; only the retries are.
    --
    -- It is also the LEASE: the delivery pass moves it forward before making
    -- any HTTP call, so a second process cannot pick up a row that is already
    -- in flight, and a process that dies mid-pass releases its rows when the
    -- lease elapses. See the claim statement in store.go for why the lease is
    -- here rather than in a transaction held open across the network.
    next_attempt_at timestamptz NOT NULL DEFAULT now(),

    -- delivered_at is when the receiver accepted it; NULL until then.
    delivered_at timestamptz,

    -- dead_lettered_at is when the sender gave up; NULL while it has not.
    dead_lettered_at timestamptz,

    -- last_error is why the last attempt failed; empty when none has.
    last_error text NOT NULL DEFAULT '',

    -- last_status is the HTTP status of the last attempt, or 0 when the
    -- request never got an answer. The distinction is the first thing a human
    -- debugging a dead letter needs: 404 is a wrong URL, 0 is a host that does
    -- not resolve, 500 is the receiver's own bug.
    last_status integer NOT NULL DEFAULT 0,

    created_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT webhook_delivery_event_not_empty CHECK (event_name <> ''),
    CONSTRAINT webhook_delivery_attempts_nonneg CHECK (attempts >= 0),
    -- A delivery is given up on or delivered, never both. The same constraint
    -- event_outbox carries, for the same reason: two closing timestamps telling
    -- different stories make the row unreadable.
    CONSTRAINT webhook_delivery_dead_letter_is_undelivered
        CHECK (dead_lettered_at IS NULL OR delivered_at IS NULL),
    -- One delivery per receiver per event. This is the idempotency of the
    -- subscriber, expressed where it cannot be forgotten.
    CONSTRAINT webhook_delivery_once_per_endpoint UNIQUE (endpoint_id, event_id)
);

-- The delivery pass's only query: undelivered, not given up on, due, oldest
-- first.
--
-- It is PARTIAL for the same reason event_outbox's is, and the reason is not
-- speed in the abstract: a delivered row and a dead row both leave the index
-- for good, so the index holds the BACKLOG rather than the history.
--
-- Measured on 2026-09-06, PostgreSQL 16, 40,000 delivered rows, 2,000 dead
-- letters and 300 pending rows, running the claim's inner SELECT under
-- EXPLAIN (ANALYZE, BUFFERS) with a LIMIT of 100, warm, best of three:
--
--   * this index                              0.039 ms,    22 buffers
--   * the same key with no partial predicate  3.271 ms, 5,073 buffers,
--                                             42,000 rows removed by filter
--
-- The number that matters is not the millisecond, it is the 42,000: those are
-- the closed rows the second plan walks past to find 100 live ones, and that
-- count grows for as long as the installation runs. It is the difference
-- between a slower query and a query that gets slower.
CREATE INDEX IF NOT EXISTS webhook_delivery_due_idx
    ON webhook_delivery (created_at, id)
    WHERE delivered_at IS NULL AND dead_lettered_at IS NULL;

-- The dead letters' own index, and it is not an optimization either.
--
-- It is what makes the pile READABLE at the cost of the pile's size rather than
-- the table's. The delivery job asks "is there anything a human has to look
-- at?" on every pass, once a minute, forever; without this index that question
-- is a sequential scan of the whole delivery history, and a question that
-- expensive is one somebody eventually stops asking. A ledger nobody reads is
-- the mistake this repository has already made once, in audit_log.
CREATE INDEX IF NOT EXISTS webhook_delivery_dead_idx
    ON webhook_delivery (dead_lettered_at, id)
    WHERE dead_lettered_at IS NOT NULL;

-- The enqueue path asks "which receivers want this topic".
--
-- GIN on the array is what answers it without reading every receiver row. The
-- table is small in every installation anyone has described, so this index is
-- not bought for a measured cost today; it is here because the alternative — a
-- sequential scan per published event — is a cost that scales with the event
-- RATE rather than with the receiver count, and the event rate is the thing an
-- installation grows.
CREATE INDEX IF NOT EXISTS webhook_endpoint_topics_idx
    ON webhook_endpoint USING gin (topics);
