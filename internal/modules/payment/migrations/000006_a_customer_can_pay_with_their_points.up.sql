-- A customer can pay with their points, and a point is worth one minor unit of
-- the currency it was earned in (ADR 0165).
--
-- # The ledger gains a second writer and three kinds
--
-- 000005 closed the point ledger's vocabulary at two kinds, both written by the
-- function that moves a collection's totals. Spending is the state machine
-- store credit already runs (000004), on this ledger: a session HOLDS points at
-- authorization, a cancel RELEASES them, a refund gives them back. The three
-- kinds are the credit ledger's three session kinds, with the same signs.
--
-- Both CHECKs are DROPPED and re-added under the SAME NAME, for the reason
-- fulfillment's 000004 wrote down: a second CHECK naming the same column would
-- be ANDed with the first, so a hold would still be refused by the original
-- constraint and the new one would look like it worked while changing nothing.
-- The names are kept because a live Go sentence may name a constraint and a gate
-- refuses a name that is dropped and not put back.
ALTER TABLE payment_loyalty_entries DROP CONSTRAINT IF EXISTS payment_loyalty_entries_kind_valid;
ALTER TABLE payment_loyalty_entries ADD CONSTRAINT payment_loyalty_entries_kind_valid
    CHECK (kind IN ('earn', 'reverse', 'hold', 'release', 'refund'));

-- The sign is still decided by the kind: the two kinds that take points away
-- are negative, the three that give them are positive.
ALTER TABLE payment_loyalty_entries DROP CONSTRAINT IF EXISTS payment_loyalty_entries_sign_matches_kind;
ALTER TABLE payment_loyalty_entries ADD CONSTRAINT payment_loyalty_entries_sign_matches_kind
    CHECK ((kind IN ('reverse', 'hold') AND points < 0)
        OR (kind IN ('earn', 'release', 'refund') AND points > 0));

-- # What a spend row references
--
-- 000005 said every row references the payment collection it was earned
-- against. That stays true of an earn and a reverse. A hold, a release and a
-- refund reference the PROVIDER'S OWN SESSION instead — never a collection —
-- because the earn target is recomputed per collection as the sum of what that
-- collection has already been written, and a hold carrying a collection id
-- would read as points already written and be earned back. The query that sums
-- a collection's points now names the two earning kinds, so the arithmetic does
-- not rest on the convention alone.

-- # The provider's own session table
--
-- It is 000004's payment_store_credit_sessions, column for column, because the
-- two tenders are ONE state machine (ADR 0165): the module reaches a provider
-- only through the contract, so the provider keeps its own record of what it
-- held, took and gave back. amount is in the ledger's unit, which is the
-- currency's minor unit — a point is worth one of them.
CREATE TABLE IF NOT EXISTS payment_loyalty_sessions (
    id                TEXT        PRIMARY KEY,
    idempotency_key   TEXT        NOT NULL,
    reference         TEXT        NOT NULL,
    -- customer_id is whose points this session spends. It is copied from the
    -- collection when the session is opened, for 000004's reason: the provider
    -- may not take the owner from data the CLIENT sent.
    customer_id       TEXT        NOT NULL,
    amount            BIGINT      NOT NULL,
    currency_code     TEXT        NOT NULL,
    status            TEXT        NOT NULL DEFAULT 'pending',
    authorized_amount BIGINT      NOT NULL DEFAULT 0,
    captured_amount   BIGINT      NOT NULL DEFAULT 0,
    refunded_amount   BIGINT      NOT NULL DEFAULT 0,
    decline_reason    TEXT,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT payment_loyalty_sessions_amount_positive   CHECK (amount > 0),
    CONSTRAINT payment_loyalty_sessions_customer_not_blank
        CHECK (length(btrim(customer_id)) > 0),
    CONSTRAINT payment_loyalty_sessions_authorized_nonneg CHECK (authorized_amount >= 0),
    CONSTRAINT payment_loyalty_sessions_captured_nonneg   CHECK (captured_amount >= 0),
    CONSTRAINT payment_loyalty_sessions_refunded_nonneg   CHECK (refunded_amount >= 0),
    CONSTRAINT payment_loyalty_sessions_captured_le_auth  CHECK (captured_amount <= authorized_amount),
    CONSTRAINT payment_loyalty_sessions_refund_le_capture CHECK (refunded_amount <= captured_amount),
    CONSTRAINT payment_loyalty_sessions_status_valid
        CHECK (status IN ('pending', 'authorized', 'captured', 'canceled', 'failed'))
);

-- The same idempotency key cannot open a SECOND session; this is what enforces
-- the contract's idempotency requirement (core/provider).
CREATE UNIQUE INDEX IF NOT EXISTS payment_loyalty_sessions_idempotency_uniq
    ON payment_loyalty_sessions (idempotency_key);
