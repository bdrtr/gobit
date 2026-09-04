-- The webpush plugin's schema: the device registry.
--
-- The schema belongs to the PLUGIN and so does its version ledger
-- (webpush_schema_migrations, see core/db.MigrationsTable): removing the plugin
-- leaves only these two tables behind and touches no module's ledger.
--
-- # Why this table exists at all
--
-- A push destination is not an address a caller can hand over. It is three
-- values the BROWSER mints, and the framework has to have stored them before a
-- send is possible. That is the whole reason web push is a module rather than a
-- notification provider (ADR 0018).

-- customer_id carries NO FOREIGN KEY (Principle 2.2: no cross-module FK).
-- A constraint here would bind the customer table to a plugin and would make
-- deleting a customer fail on a device row. It is free text, and its truth is
-- the same unverified claim ADR 0008 governs.
CREATE TABLE IF NOT EXISTS webpush_subscription (
    -- id is the row's identity (wps_...). It is what the admin remediation
    -- endpoint deletes by.
    id text PRIMARY KEY,

    -- endpoint is the push service URL the browser minted. It is UNIQUE, and
    -- the uniqueness is what makes a re-subscribe an update rather than a
    -- second row for the same device.
    endpoint text NOT NULL UNIQUE,

    -- p256dh is the device's public key and auth is its secret, both base64url
    -- as the browser produced them. They are OVERWRITTEN on every re-subscribe:
    -- they are what the browser just minted, and keeping an older pair means
    -- encrypting messages that device can no longer open.
    p256dh text NOT NULL,
    auth text NOT NULL,

    -- customer_id is empty for a device that subscribed before signing in.
    -- It is never downgraded to empty by an upsert (a returning browser sends
    -- no customer id), which is exactly why the unbind endpoint has to exist:
    -- once the upsert refuses to clear it, nothing else can, and a shared
    -- device would keep delivering the previous user's orders forever.
    customer_id text NOT NULL DEFAULT '',

    -- locale selects the template. Empty means the default template.
    locale text NOT NULL DEFAULT '',

    -- vapid_fingerprint records WHICH signing key this subscription was minted
    -- under. It exists because the VAPID private key is durable state on the
    -- order of the database: rotate it and every row here can only ever answer
    -- 401. A 401 must never delete a row (a temporary auth fault would wipe the
    -- table), so without this column a rotation leaves an invisible graveyard.
    vapid_fingerprint text NOT NULL,

    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- THERE IS NO deleted_at, and its absence is the decision.
--
-- A soft-deleted row keeps occupying the unique endpoint key, so the browser
-- that unsubscribed could never subscribe again — it re-issues the SAME
-- endpoint string. The notification module's own schema records the same
-- reasoning for the same reason.

-- The order.placed handler looks devices up by customer.
CREATE INDEX IF NOT EXISTS webpush_subscription_customer_idx
    ON webpush_subscription (customer_id)
    WHERE customer_id <> '';

-- The startup sweep counts rows minted under a key that is no longer held.
CREATE INDEX IF NOT EXISTS webpush_subscription_fingerprint_idx
    ON webpush_subscription (vapid_fingerprint);
