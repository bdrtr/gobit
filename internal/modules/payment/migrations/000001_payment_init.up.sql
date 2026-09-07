-- The payment module's schema (plan Phase 6).
--
-- Ownership: the five tables here belong to the payment module ALONE. Foreign
-- keys inside the module are free and are used; no other module's table is ever
-- REFERENCED (Principle 2.2 — the cross-module FK ban). That is why
-- payment_collections.reference (a cart or an order identifier) is NOT an FK:
-- the relation is made through Module Links, and the link table is core's.
--
-- Money: every amount is a BIGINT in minor units (cents); the currency lives in
-- a SEPARATE column (plan Section 8). NUMERIC and floating point are used
-- nowhere — a one-cent difference between what was collected and what was
-- reconciled is found at the end of the day, by hand, and only then.
--
-- Time: every stamp is timestamptz (UTC). Deletion is soft (deleted_at) and
-- every read query applies the deleted_at IS NULL filter.

-- payment_collections is the vessel for the payments collected against one cart
-- or order.
--
-- The status is STORED but it is DERIVED: after every mutation the service
-- recomputes it from the amounts and the session counts and writes it back (see
-- service.CollectionStatusFor). The column exists so that the state can be
-- queried; the source of truth is the amounts.
CREATE TABLE IF NOT EXISTS payment_collections (
    id                TEXT        PRIMARY KEY,
    -- reference is the identifier of the caller's OWN record (a cart or an
    -- order). There is NO FK (Principle 2.2); the bond is made through Module
    -- Links.
    reference         TEXT        NOT NULL,
    amount            BIGINT      NOT NULL,
    currency_code     TEXT        NOT NULL,
    status            TEXT        NOT NULL DEFAULT 'not_paid',
    -- The three amounts below are the sums of the session, capture and refund
    -- records, and they are updated under the collection row's lock.
    authorized_amount BIGINT      NOT NULL DEFAULT 0,
    captured_amount   BIGINT      NOT NULL DEFAULT 0,
    refunded_amount   BIGINT      NOT NULL DEFAULT 0,
    metadata          JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at        TIMESTAMPTZ,

    CONSTRAINT payment_collections_amount_positive     CHECK (amount > 0),
    CONSTRAINT payment_collections_authorized_nonneg   CHECK (authorized_amount >= 0),
    CONSTRAINT payment_collections_captured_nonneg     CHECK (captured_amount >= 0),
    CONSTRAINT payment_collections_refunded_nonneg     CHECK (refunded_amount >= 0),
    -- The refunded amount may NOT EXCEED the captured one. The service already
    -- refuses that; the constraint here is the last defence: not even an edit
    -- made straight in SQL can refund money that was never taken.
    CONSTRAINT payment_collections_refund_le_capture   CHECK (refunded_amount <= captured_amount),
    -- What is authorized and what is captured may not exceed the collection's
    -- AMOUNT. The collection is the ceiling on the money to be collected: a
    -- total above it means taking more from the customer than the order. The
    -- service already refuses that (the amount open sessions have reserved is
    -- subtracted as well); the constraint here is the last defence and stops an
    -- edit made straight in SQL too.
    CONSTRAINT payment_collections_authorized_le_amount CHECK (authorized_amount <= amount),
    CONSTRAINT payment_collections_captured_le_amount   CHECK (captured_amount <= amount),
    CONSTRAINT payment_collections_currency_format     CHECK (currency_code ~ '^[A-Z]{3}$'),
    CONSTRAINT payment_collections_status_valid
        CHECK (status IN ('not_paid', 'awaiting', 'authorized',
                          'partially_captured', 'captured',
                          'partially_refunded', 'refunded', 'canceled'))
);

CREATE INDEX IF NOT EXISTS payment_collections_reference_idx
    ON payment_collections (reference)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS payment_collections_alive_idx
    ON payment_collections (created_at DESC, id DESC)
    WHERE deleted_at IS NULL;

-- payment_sessions is a payment session opened AT A PROVIDER.
--
-- external_id is the provider's own identifier; it is the field that matches
-- the two systems up during reconciliation. data is the raw body the provider
-- returned, and it is stored without being interpreted.
CREATE TABLE IF NOT EXISTS payment_sessions (
    id                    TEXT        PRIMARY KEY,
    payment_collection_id TEXT        NOT NULL REFERENCES payment_collections (id) ON DELETE CASCADE,
    provider_id           TEXT        NOT NULL,
    external_id           TEXT        NOT NULL,
    status                TEXT        NOT NULL DEFAULT 'pending',
    amount                BIGINT      NOT NULL,
    authorized_amount     BIGINT      NOT NULL DEFAULT 0,
    currency_code         TEXT        NOT NULL,
    data                  JSONB       NOT NULL DEFAULT '{}'::jsonb,
    -- idempotency_key stops the same session from being opened twice (plan
    -- Section 2.6).
    idempotency_key       TEXT        NOT NULL,
    -- decline_reason is filled only while status = 'failed'; it is for
    -- diagnosis, not for showing to the customer.
    decline_reason        TEXT,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at            TIMESTAMPTZ,

    CONSTRAINT payment_sessions_amount_positive   CHECK (amount > 0),
    CONSTRAINT payment_sessions_authorized_nonneg CHECK (authorized_amount >= 0),
    CONSTRAINT payment_sessions_authorized_le_amount CHECK (authorized_amount <= amount),
    CONSTRAINT payment_sessions_currency_format   CHECK (currency_code ~ '^[A-Z]{3}$'),
    CONSTRAINT payment_sessions_status_valid
        CHECK (status IN ('pending', 'authorized', 'captured', 'canceled', 'failed'))
);

-- A (provider, idempotency key) pair is unique among the LIVING sessions. When
-- the saga retries a step, the second CreateSession finds the existing session
-- BEFORE it ever reaches this index; the index is the last defence and it
-- guarantees that of two concurrent opens only one writes a row.
CREATE UNIQUE INDEX IF NOT EXISTS payment_sessions_provider_idempotency_uniq
    ON payment_sessions (provider_id, idempotency_key)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS payment_sessions_collection_idx
    ON payment_sessions (payment_collection_id, created_at DESC)
    WHERE deleted_at IS NULL;

-- payments is a capture that actually happened.
--
-- AT MOST ONE capture comes out of a session; a partial capture closes the
-- session. The unique index enforces that, and it is what makes Capture
-- idempotent: the second call writes no new row, it returns the existing one.
CREATE TABLE IF NOT EXISTS payments (
    id                    TEXT        PRIMARY KEY,
    payment_session_id    TEXT        NOT NULL REFERENCES payment_sessions (id) ON DELETE CASCADE,
    payment_collection_id TEXT        NOT NULL REFERENCES payment_collections (id) ON DELETE CASCADE,
    amount                BIGINT      NOT NULL,
    currency_code         TEXT        NOT NULL,
    refunded_amount       BIGINT      NOT NULL DEFAULT 0,
    captured_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at            TIMESTAMPTZ,

    CONSTRAINT payments_amount_positive    CHECK (amount > 0),
    CONSTRAINT payments_refunded_nonneg    CHECK (refunded_amount >= 0),
    CONSTRAINT payments_refund_le_amount   CHECK (refunded_amount <= amount),
    CONSTRAINT payments_currency_format    CHECK (currency_code ~ '^[A-Z]{3}$')
);

CREATE UNIQUE INDEX IF NOT EXISTS payments_session_uniq
    ON payments (payment_session_id)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS payments_collection_idx
    ON payments (payment_collection_id, created_at DESC)
    WHERE deleted_at IS NULL;

-- refunds is the repayment of a capture. A partial refund produces several
-- rows; their sum is kept in the payments.refunded_amount column.
CREATE TABLE IF NOT EXISTS refunds (
    id         TEXT        PRIMARY KEY,
    payment_id TEXT        NOT NULL REFERENCES payments (id) ON DELETE CASCADE,
    amount     BIGINT      NOT NULL,
    reason     TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ,

    CONSTRAINT refunds_amount_positive CHECK (amount > 0)
);

CREATE INDEX IF NOT EXISTS refunds_payment_idx
    ON refunds (payment_id, created_at DESC)
    WHERE deleted_at IS NULL;

-- payment_manual_sessions is the MANUAL provider's own ledger.
--
-- Why a SEPARATE table: the manual provider IMITATES a real payment
-- institution. A real provider's state lives in its own system, and the module
-- reaches it only through the PaymentProvider interface. Keeping the same
-- separation here makes it structurally impossible for the module to read the
-- provider's internal state by accident: the payment service NEVER touches this
-- table.
--
-- Why NOT IN MEMORY: the e2e flows and the Phase 9 load test have to find an
-- opened session again after the process restarts. A session held in memory
-- would produce "session not found" on every restart of the server, and the
-- saga's compensating step (Cancel) could not run in exactly the scenario where
-- the process went down.
--
-- There is NO soft delete: this table is not the module's domain data, it is
-- the ledger of the imitated outside system; its records are never deleted.
CREATE TABLE IF NOT EXISTS payment_manual_sessions (
    id                TEXT        PRIMARY KEY,
    idempotency_key   TEXT        NOT NULL,
    reference         TEXT        NOT NULL,
    amount            BIGINT      NOT NULL,
    currency_code     TEXT        NOT NULL,
    status            TEXT        NOT NULL DEFAULT 'pending',
    authorized_amount BIGINT      NOT NULL DEFAULT 0,
    captured_amount   BIGINT      NOT NULL DEFAULT 0,
    refunded_amount   BIGINT      NOT NULL DEFAULT 0,
    data              JSONB       NOT NULL DEFAULT '{}'::jsonb,
    decline_reason    TEXT,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT payment_manual_sessions_amount_positive    CHECK (amount > 0),
    CONSTRAINT payment_manual_sessions_authorized_nonneg  CHECK (authorized_amount >= 0),
    CONSTRAINT payment_manual_sessions_captured_nonneg    CHECK (captured_amount >= 0),
    CONSTRAINT payment_manual_sessions_refunded_nonneg    CHECK (refunded_amount >= 0),
    CONSTRAINT payment_manual_sessions_captured_le_auth   CHECK (captured_amount <= authorized_amount),
    CONSTRAINT payment_manual_sessions_refund_le_capture  CHECK (refunded_amount <= captured_amount),
    CONSTRAINT payment_manual_sessions_status_valid
        CHECK (status IN ('pending', 'authorized', 'captured', 'canceled', 'failed'))
);

-- The same idempotency key cannot open a SECOND session. This constraint is
-- what ultimately enforces the idempotency requirement of the provider contract
-- (core/provider).
CREATE UNIQUE INDEX IF NOT EXISTS payment_manual_sessions_idempotency_uniq
    ON payment_manual_sessions (idempotency_key);
