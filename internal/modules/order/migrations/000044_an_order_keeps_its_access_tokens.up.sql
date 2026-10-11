-- An order keeps the digest of every access token it handed out (ADR 0439).
--
-- A token opens the order at the storefront, which its id no longer does: the
-- id goes to every webhook receiver of an order topic. One is issued to the
-- caller that completed the cart and one to every mail that names the order,
-- so an order holds a few; the plaintext leaves once, to its recipient, and is
-- stored nowhere. token_hash is SHA-256 of the plaintext. A token is 256
-- random bits, so a plain digest is enough and a slow hash would add nothing.
--
-- The row is the order's own and goes with it. An erasure deletes them, so a
-- forgotten person's links open nothing.
CREATE TABLE IF NOT EXISTS order_access_tokens (
    order_id   TEXT        NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    token_hash BYTEA       NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (order_id, token_hash),
    CONSTRAINT order_access_tokens_hash_length CHECK (octet_length(token_hash) = 32)
);
