-- The paytr plugin's schema: what PayTR told us about each payment.
--
-- The schema belongs to the PLUGIN and so does its version ledger
-- (paytr_schema_migrations): removing the plugin leaves only these two tables
-- and touches no module's ledger.
--
-- # Why a callback-driven provider needs a table at all
--
-- The provider contract asks `Authorize(ctx, sessionID)` — "is the money held?"
-- — and expects the provider to answer from the session id alone. A gateway
-- that can be DRIVEN (Stripe) answers by making an API call. PayTR cannot be
-- driven: the customer pays inside PayTR's own iframe and PayTR reports the
-- outcome by posting back to us, once, at a moment of its choosing.
--
-- So the answer to "is the money held?" is whatever the callback said, and it
-- has to survive a restart between the callback and the question. In-memory
-- state would work in development and lose payments in production.
--
-- This is the same finding ADR 0018 recorded for web push, arriving from the
-- other direction: a provider slot alone cannot express a party that reports
-- back rather than being asked.
CREATE TABLE IF NOT EXISTS paytr_payment (
    -- merchant_oid is the reference PayTR knows the payment by, and it is the
    -- payment session's external id on this side. It is the ONLY join between
    -- the two systems, which is why it is the primary key.
    merchant_oid text PRIMARY KEY,

    -- amount is what the session was opened for, in minor units. The callback's
    -- total_amount is compared against it: a callback whose amount disagrees is
    -- recorded and NOT treated as payment for this session.
    amount bigint NOT NULL,
    currency_code text NOT NULL,

    -- status is what PayTR last told us: pending until a callback arrives, then
    -- success or failed.
    status text NOT NULL DEFAULT 'pending',

    -- paid_amount is the total_amount the callback carried, in minor units. It
    -- is kept even when it disagrees with amount, because the disagreement is
    -- the thing an operator has to be able to see.
    paid_amount bigint NOT NULL DEFAULT 0,

    -- failure_reason is what PayTR said when the payment did not succeed. It is
    -- shown to nobody automatically; it is here so a support question has an
    -- answer.
    failure_reason text NOT NULL DEFAULT '',

    -- refunded_amount accumulates what has been sent back, in minor units.
    -- PayTR has no "how much has been refunded" query, so the only ledger of
    -- that is this column.
    refunded_amount bigint NOT NULL DEFAULT 0,

    created_at timestamptz NOT NULL DEFAULT now(),
    -- callback_at is when PayTR reported, and it is NULL until then. It answers
    -- the question a stuck checkout raises: did PayTR ever call us at all?
    callback_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- The operator's question is almost always "which payments are stuck", so the
-- index is on the status of the ones that have not reported yet.
CREATE INDEX IF NOT EXISTS paytr_payment_pending_idx
    ON paytr_payment (created_at)
    WHERE status = 'pending';
