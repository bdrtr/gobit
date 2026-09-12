-- A shop can hold money for a customer, and the customer can spend it.
--
-- # Why a LEDGER and not a balance column
--
-- A balance is the sum of what happened to it, and a column is that sum with the
-- history thrown away. The question an operator is asked about store credit is
-- never "how much is left" alone — it is "why", and the answer is a refund that
-- became credit, a goodwill gesture, an order that spent it. A column answers the
-- first question and loses the second one permanently.
--
-- Every row is an EVENT with a signed amount, and the balance is their sum. That
-- also makes the arithmetic auditable: a balance that looks wrong can be
-- explained line by line rather than argued about.
--
-- # Why it lives in the payment module
--
-- Because store credit is a way of PAYING. It is spent through the same provider
-- slot a card is spent through (ADR 0152), so the module that owns sessions,
-- captures and refunds is the one that owns this too. Putting it in the customer
-- module would have made the customer module a party to money movements, and the
-- checkout saga would then have two modules to compensate instead of one.
CREATE TABLE IF NOT EXISTS payment_store_credit_entries (
    id            TEXT        PRIMARY KEY,
    -- customer_id is the person the money belongs to. There is NO FK
    -- (Principle 2.2): the customer module owns that record and this module may
    -- not depend on its schema.
    customer_id   TEXT        NOT NULL,
    currency_code TEXT        NOT NULL,

    -- amount is SIGNED minor units and the balance is SUM(amount).
    --
    -- The sign is not free: it is decided by the kind and a CHECK holds the
    -- pairing, so a hold that was written positive — the mistake that hands a
    -- customer money for spending it — cannot enter the table at all.
    amount        BIGINT      NOT NULL,

    -- kind says what happened. The vocabulary is closed by a CHECK because a
    -- misspelled kind would still sum correctly and would be invisible in the
    -- balance while making the history unreadable.
    --
    --   issue   (+) an operator gave the customer money
    --   hold    (-) a payment session put some of it aside
    --   release (+) that session was canceled and the hold came back
    --   refund  (+) a captured payment was repaid into the credit
    kind          TEXT        NOT NULL,

    -- reference is the payment session the row belongs to, for the three kinds
    -- that have one. An issue carries the operator's own reference or nothing.
    reference     TEXT        NOT NULL DEFAULT '',
    -- reason is why an operator issued credit; it is shown to nobody but an
    -- operator and it is the half a balance column cannot keep.
    reason        TEXT        NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT payment_store_credit_entries_customer_not_blank
        CHECK (length(btrim(customer_id)) > 0),
    CONSTRAINT payment_store_credit_entries_currency_not_blank
        CHECK (length(btrim(currency_code)) > 0),
    CONSTRAINT payment_store_credit_entries_amount_not_zero
        CHECK (amount <> 0),
    CONSTRAINT payment_store_credit_entries_kind_valid
        CHECK (kind IN ('issue', 'hold', 'release', 'refund')),
    CONSTRAINT payment_store_credit_entries_sign_matches_kind
        CHECK ((kind = 'hold' AND amount < 0) OR (kind <> 'hold' AND amount > 0))
);

-- The balance read, and the lock the authorization takes.
--
-- Both walk one customer's rows in one currency, which is the only access shape
-- this table has: nothing lists "all credit" and nothing reads a row by id.
CREATE INDEX IF NOT EXISTS payment_store_credit_entries_customer_idx
    ON payment_store_credit_entries (customer_id, currency_code, created_at);

-- A session at the store-credit provider, which is the provider's OWN ledger.
--
-- It is separate from payment_sessions for the reason payment_manual_sessions is:
-- the module reaches a provider only through the core contract, and a provider
-- that read the module's own tables would make the separation a fiction. The
-- shape follows the manual provider's table because the state machine is the
-- contract's, not this provider's.
CREATE TABLE IF NOT EXISTS payment_store_credit_sessions (
    id                TEXT        PRIMARY KEY,
    idempotency_key   TEXT        NOT NULL,
    reference         TEXT        NOT NULL,
    -- customer_id is whose money this session spends. It is what the whole
    -- decision turns on: without it the provider would have to take the customer
    -- from data the CLIENT sent, and a shopper could spend somebody else's
    -- credit by naming them (ADR 0152).
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

    CONSTRAINT payment_store_credit_sessions_amount_positive   CHECK (amount > 0),
    CONSTRAINT payment_store_credit_sessions_customer_not_blank
        CHECK (length(btrim(customer_id)) > 0),
    CONSTRAINT payment_store_credit_sessions_authorized_nonneg CHECK (authorized_amount >= 0),
    CONSTRAINT payment_store_credit_sessions_captured_nonneg   CHECK (captured_amount >= 0),
    CONSTRAINT payment_store_credit_sessions_refunded_nonneg   CHECK (refunded_amount >= 0),
    CONSTRAINT payment_store_credit_sessions_captured_le_auth  CHECK (captured_amount <= authorized_amount),
    CONSTRAINT payment_store_credit_sessions_refund_le_capture CHECK (refunded_amount <= captured_amount),
    CONSTRAINT payment_store_credit_sessions_status_valid
        CHECK (status IN ('pending', 'authorized', 'captured', 'canceled', 'failed'))
);

-- The same idempotency key cannot open a SECOND session; this is what enforces
-- the contract's idempotency requirement (core/provider).
CREATE UNIQUE INDEX IF NOT EXISTS payment_store_credit_sessions_idempotency_uniq
    ON payment_store_credit_sessions (idempotency_key);

-- A payment collection records WHOSE money it collects.
--
-- It is nullable because most collections have no customer: a guest cart pays
-- with a card and nobody is named. What needs it is a tender whose funds belong
-- to a PERSON — store credit today, loyalty points tomorrow — and the alternative
-- was for such a provider to read the customer out of client-supplied data, which
-- is a shopper spending somebody else's balance by typing their id (ADR 0152).
ALTER TABLE payment_collections
    ADD COLUMN IF NOT EXISTS customer_id TEXT;

ALTER TABLE payment_collections
    ADD CONSTRAINT payment_collections_customer_not_blank
    CHECK (customer_id IS NULL OR length(btrim(customer_id)) > 0);
