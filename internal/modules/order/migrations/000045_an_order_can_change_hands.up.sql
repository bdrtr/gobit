-- An order can change hands (ADR 0450).
--
-- A transfer moves an order from the customer it belongs to, or from a guest,
-- to another customer. A shopper asks for it at the storefront and the
-- address the order is mailed at says yes or no with a token sent there; an
-- operator moves an order at once. Every transfer is a row here, the
-- storefront's while it waits and the operator's already closed, so the order
-- keeps who held it before.
--
-- token_hash is SHA-256 of the token mailed to from_email, as an order's
-- access tokens are kept (ADR 0439); the plaintext leaves once, to that
-- address. An operator's move mails nothing and holds none.
--
-- from_customer_id is the order's customer when the request was written,
-- NULL for a guest's order; from_email is the address the order held then,
-- the one asked. Acceptance moves the order only while it still holds both, so
-- a request written before another move cannot undo it. to_customer_id is the
-- customer who receives the order. The three are personal data the module
-- declares; an erasure empties from_email and keeps the two ids, as it keeps
-- orders.customer_id.
--
-- expires_at bounds a storefront request. Nothing sweeps it: a request past
-- it stays 'requested' and is read as expired, and it neither holds the order
-- nor can be accepted. closed_at is when the request stopped waiting.
--
-- Orders are never deleted (000010), so a transfer has no ON DELETE.
CREATE TABLE IF NOT EXISTS order_transfers (
    id               TEXT        PRIMARY KEY,
    order_id         TEXT        NOT NULL REFERENCES orders (id),
    status           TEXT        NOT NULL,
    requested_by     TEXT        NOT NULL,
    from_customer_id TEXT,
    to_customer_id   TEXT        NOT NULL,
    from_email       TEXT,
    token_hash       BYTEA,
    expires_at       TIMESTAMPTZ,
    closed_at        TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT order_transfers_status_known
        CHECK (status IN ('requested', 'accepted', 'declined', 'canceled')),
    CONSTRAINT order_transfers_requested_by_known
        CHECK (requested_by IN ('customer', 'operator')),
    -- A storefront request carries its token and its bound; an operator's move
    -- carries neither and is accepted when it is written.
    CONSTRAINT order_transfers_request_shape
        CHECK ((requested_by = 'customer') = (token_hash IS NOT NULL AND expires_at IS NOT NULL)),
    CONSTRAINT order_transfers_operator_accepted
        CHECK (requested_by = 'customer' OR status = 'accepted'),
    CONSTRAINT order_transfers_hash_length
        CHECK (token_hash IS NULL OR octet_length(token_hash) = 32),
    CONSTRAINT order_transfers_closed_when_answered
        CHECK ((status = 'requested') = (closed_at IS NULL)),
    CONSTRAINT order_transfers_to_another
        CHECK (from_customer_id IS DISTINCT FROM to_customer_id),
    CONSTRAINT order_transfers_ids_not_blank
        CHECK (length(btrim(to_customer_id)) > 0
            AND (from_customer_id IS NULL OR length(btrim(from_customer_id)) > 0))
);

-- The order's transfers, newest first: the admin listing and the search for a
-- request still waiting.
CREATE INDEX IF NOT EXISTS order_transfers_order_idx
    ON order_transfers (order_id, created_at DESC, id DESC);

-- A person's transfers, for an erasure and a disclosure that name them.
CREATE INDEX IF NOT EXISTS order_transfers_from_customer_idx
    ON order_transfers (from_customer_id)
    WHERE from_customer_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS order_transfers_to_customer_idx
    ON order_transfers (to_customer_id);
CREATE INDEX IF NOT EXISTS order_transfers_from_email_idx
    ON order_transfers (from_email)
    WHERE from_email IS NOT NULL;
