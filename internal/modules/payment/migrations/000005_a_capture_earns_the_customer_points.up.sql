-- A capture earns the customer points, and a refund takes them back.
--
-- # Why a LEDGER and not a points column
--
-- For 000004's reason, one table over: a balance is the sum of what happened to
-- it, and a column is that sum with the history thrown away. The question an
-- operator is asked about a point balance is "why does this customer have four
-- hundred", and the answer is a list of the captures that earned them and the
-- refunds that pulled them back. A column answers "how much" and loses "why"
-- permanently.
--
-- # Why the payment module owns it
--
-- Because the points are earned from MONEY THAT MOVED, and the only row that
-- says whose money moved is this module's own. A payment collection carries the
-- customer, the currency and the cumulative captured and refunded totals; the
-- module's published read surface carries no customer at all, so any other
-- module would have had to be handed one — either by widening a name promised
-- until 1.0.0 or by walking back through the order link. The module that owns
-- the money moment owns the ledger that is derived from it (ADR 0164).
--
-- # Why the points are a TARGET rather than a running total
--
-- Every row is the DIFFERENCE between what this collection should have earned
-- and what it has already been written. That is what makes a second write for
-- the same collection append nothing rather than double the points: the writer
-- computes where the balance should BE, not how much to add. A refund lowers the
-- target and the difference comes out negative, which is the reverse row.
CREATE TABLE IF NOT EXISTS payment_loyalty_entries (
    id            TEXT        PRIMARY KEY,
    -- customer_id is the person the points belong to. There is NO FK
    -- (Principle 2.2): the customer module owns that record and this module may
    -- not depend on its schema. A collection with no customer — a guest paying
    -- with a card — writes no row here at all.
    customer_id   TEXT        NOT NULL,
    -- currency_code is the currency the money moved in. The points are unitless
    -- but the money they were earned from is not, and a customer who paid in two
    -- currencies has two balances rather than one meaningless sum.
    currency_code TEXT        NOT NULL,

    -- points is SIGNED and the balance is SUM(points).
    --
    -- The sign is not free: it is decided by the kind and a CHECK holds the
    -- pairing, so a refund that was written positive — the mistake that hands a
    -- customer points for taking their money back — cannot enter the table at
    -- all.
    points        BIGINT      NOT NULL,

    -- kind says what happened. The vocabulary is closed by a CHECK because a
    -- misspelled kind would still sum correctly and would be invisible in the
    -- balance while making the history unreadable.
    --
    --   earn    (+) the collection's earned target rose, because money was taken
    --   reverse (-) the target fell, because money was sent back
    kind          TEXT        NOT NULL,

    -- reference is the payment collection the row was earned against. EVERY row
    -- has one: the target is recomputed per collection on every money write, and
    -- this column is what makes that sum possible.
    reference     TEXT        NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT payment_loyalty_entries_customer_not_blank
        CHECK (length(btrim(customer_id)) > 0),
    -- The format rather than 000004's not-blank: a constraint whose name ends in
    -- _currency_format is turned into a typed Invalid error by the repository,
    -- and the credit ledger's _not_blank falls through to a 500.
    CONSTRAINT payment_loyalty_entries_currency_format
        CHECK (currency_code ~ '^[A-Z]{3}$'),
    CONSTRAINT payment_loyalty_entries_reference_not_blank
        CHECK (length(btrim(reference)) > 0),
    CONSTRAINT payment_loyalty_entries_points_not_zero
        CHECK (points <> 0),
    CONSTRAINT payment_loyalty_entries_kind_valid
        CHECK (kind IN ('earn', 'reverse')),
    CONSTRAINT payment_loyalty_entries_sign_matches_kind
        CHECK ((kind = 'reverse' AND points < 0) OR (kind = 'earn' AND points > 0))
);

-- The balance read and the operator's listing walk one customer's rows in one
-- currency, which is 000004's access shape and is served the same way.
CREATE INDEX IF NOT EXISTS payment_loyalty_entries_customer_idx
    ON payment_loyalty_entries (customer_id, currency_code, created_at);

-- The target sum walks ONE collection's rows, and unlike anything in 000004 it
-- is on the hot path: every write that moves a collection's totals takes this
-- sum first, inside the collection's own lock.
CREATE INDEX IF NOT EXISTS payment_loyalty_entries_reference_idx
    ON payment_loyalty_entries (reference);
