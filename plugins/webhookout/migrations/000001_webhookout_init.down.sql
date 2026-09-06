-- Rolling the outbound webhook schema back.
--
-- The indexes fall with their tables; they are dropped EXPLICITLY anyway,
-- because the name space is shared with tables and the day one of them moves to
-- another table this file would silently be incomplete.
--
-- THE DATA LOSS HERE IS REAL and it is of two different kinds, which is why it
-- is written out rather than assumed:
--
--   * The receivers and their SECRETS are gone. A receiver can be registered
--     again, but the secret is minted by the server and returned exactly once,
--     so every receiver has to be re-issued one and every consumer of this
--     plugin has to be reconfigured. Nothing on this side can repair that.
--   * The undelivered and dead-lettered deliveries are gone. Those are events
--     somebody was owed and was never sent; after this migration nothing
--     records that they were owed. Read the dead-letter listing before rolling
--     back — `GET /admin/v1/webhooks/deliveries?state=dead` — because
--     afterwards there is nowhere to read it from.
--
-- It is still a correct down migration: a reversible schema change is the
-- contract, and an irreversible one would leave an operator unable to move
-- between versions at all. The cost is written here so it is not discovered
-- afterwards.
DROP INDEX IF EXISTS webhook_endpoint_topics_idx;
DROP INDEX IF EXISTS webhook_delivery_dead_idx;
DROP INDEX IF EXISTS webhook_delivery_due_idx;
DROP TABLE IF EXISTS webhook_delivery;
DROP TABLE IF EXISTS webhook_endpoint;
