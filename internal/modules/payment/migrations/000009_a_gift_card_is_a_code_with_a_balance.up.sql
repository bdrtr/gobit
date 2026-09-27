-- A gift card is a code that holds a balance (ADR 0208).
--
-- It is the third balance this module keeps, beside store credit (000004) and
-- loyalty points (000005), and it is spent through the same state machine. What
-- differs is the owner: a card belongs to whoever presents its code, so the card
-- is a row of its own and the ledger and the sessions name the card rather than
-- a customer.
CREATE TABLE IF NOT EXISTS payment_gift_cards (
    id            TEXT        PRIMARY KEY,
    -- code_digest is the SHA-256 of the normalized code, in hex. The code itself
    -- is a bearer credential and is kept nowhere: it is shown once, when the card
    -- is issued, and a card is found again by the digest of what is presented.
    code_digest   TEXT        NOT NULL,
    -- code_tail is the code's last four characters, so an operator can tell
    -- cards apart without the shop keeping what spends them.
    code_tail     TEXT        NOT NULL,
    -- currency_code is the one currency the card holds; a card in one currency
    -- pays nothing in another.
    currency_code TEXT        NOT NULL,
    -- reason is why an operator issued the card, required as store credit's
    -- reason is: it is the half of the record nothing else would keep.
    reason        TEXT        NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT payment_gift_cards_digest_is_sha256
        CHECK (code_digest ~ '^[0-9a-f]{64}$'),
    CONSTRAINT payment_gift_cards_tail_four
        CHECK (length(code_tail) = 4),
    CONSTRAINT payment_gift_cards_currency_not_blank
        CHECK (length(btrim(currency_code)) > 0),
    CONSTRAINT payment_gift_cards_reason_not_blank
        CHECK (length(btrim(reason)) > 0)
);

-- One code opens one card: the digest is how a presented code finds its card.
CREATE UNIQUE INDEX IF NOT EXISTS payment_gift_cards_code_digest_uniq
    ON payment_gift_cards (code_digest);

-- The events on a card's balance; the balance is their sum, as store credit's.
CREATE TABLE IF NOT EXISTS payment_gift_card_entries (
    id           TEXT        PRIMARY KEY,
    gift_card_id TEXT        NOT NULL REFERENCES payment_gift_cards (id),
    -- amount is SIGNED minor units of the card's currency, its sign decided by
    -- the kind:
    --
    --   issue   (+) the balance the card was issued with
    --   hold    (-) a payment session put some of it aside
    --   release (+) that session was canceled and the hold came back
    --   refund  (+) a captured payment was repaid onto the card
    amount       BIGINT      NOT NULL,
    kind         TEXT        NOT NULL,
    -- reference is the payment session the row belongs to; empty on the issue.
    reference    TEXT        NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT payment_gift_card_entries_amount_not_zero
        CHECK (amount <> 0),
    CONSTRAINT payment_gift_card_entries_kind_valid
        CHECK (kind IN ('issue', 'hold', 'release', 'refund')),
    CONSTRAINT payment_gift_card_entries_sign_matches_kind
        CHECK ((kind = 'hold' AND amount < 0) OR (kind <> 'hold' AND amount > 0)),
    CONSTRAINT payment_gift_card_entries_session_named
        CHECK ((kind = 'issue') = (reference = ''))
);

-- A card is issued once: a second issue row would be money nobody decided on.
CREATE UNIQUE INDEX IF NOT EXISTS payment_gift_card_entries_one_issue
    ON payment_gift_card_entries (gift_card_id) WHERE kind = 'issue';

-- The balance read walks one card's rows.
CREATE INDEX IF NOT EXISTS payment_gift_card_entries_card_idx
    ON payment_gift_card_entries (gift_card_id, created_at);

-- The journal reads the issues by time, as it reads store credit's (000007).
CREATE INDEX IF NOT EXISTS payment_gift_card_entries_issued_at_idx
    ON payment_gift_card_entries (created_at, id)
    WHERE kind = 'issue';

-- A session at the gift-card provider, the provider's OWN ledger, for the reason
-- payment_store_credit_sessions is one.
CREATE TABLE IF NOT EXISTS payment_gift_card_sessions (
    id                TEXT        PRIMARY KEY,
    idempotency_key   TEXT        NOT NULL,
    reference         TEXT        NOT NULL,
    -- gift_card_id is the card this session spends, found from the code the
    -- payment presented.
    gift_card_id      TEXT        NOT NULL REFERENCES payment_gift_cards (id),
    amount            BIGINT      NOT NULL,
    currency_code     TEXT        NOT NULL,
    status            TEXT        NOT NULL DEFAULT 'pending',
    authorized_amount BIGINT      NOT NULL DEFAULT 0,
    captured_amount   BIGINT      NOT NULL DEFAULT 0,
    refunded_amount   BIGINT      NOT NULL DEFAULT 0,
    decline_reason    TEXT,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT payment_gift_card_sessions_amount_positive   CHECK (amount > 0),
    CONSTRAINT payment_gift_card_sessions_authorized_nonneg CHECK (authorized_amount >= 0),
    CONSTRAINT payment_gift_card_sessions_captured_nonneg   CHECK (captured_amount >= 0),
    CONSTRAINT payment_gift_card_sessions_refunded_nonneg   CHECK (refunded_amount >= 0),
    CONSTRAINT payment_gift_card_sessions_captured_le_auth  CHECK (captured_amount <= authorized_amount),
    CONSTRAINT payment_gift_card_sessions_refund_le_capture CHECK (refunded_amount <= captured_amount),
    CONSTRAINT payment_gift_card_sessions_status_valid
        CHECK (status IN ('pending', 'authorized', 'captured', 'canceled', 'failed'))
);

CREATE UNIQUE INDEX IF NOT EXISTS payment_gift_card_sessions_idempotency_uniq
    ON payment_gift_card_sessions (idempotency_key);
