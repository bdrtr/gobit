-- Rolling the webpush schema back.
--
-- The indexes fall with the table; they are dropped EXPLICITLY anyway, because
-- the name space is shared with tables and the day one of them moves to another
-- table this file would silently be incomplete.
--
-- THE DATA LOSS HERE IS REAL and cannot be undone by the application. Unlike
-- searchpg's index — which is a derived view of the catalog and can be rebuilt
-- with one admin call — a subscription can only be re-created by the BROWSER
-- that owns it. Rolling this migration back means every user has to visit the
-- site and grant permission again, and nothing on the server can trigger that.
--
-- It is still a correct down migration: a reversible schema change is the
-- contract, and an irreversible one would leave an operator unable to move
-- between versions at all. The cost is written here so it is not discovered
-- afterwards.
DROP INDEX IF EXISTS webpush_subscription_fingerprint_idx;
DROP INDEX IF EXISTS webpush_subscription_customer_idx;
DROP TABLE IF EXISTS webpush_subscription;
