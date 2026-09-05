-- order_addresses is where the order SHIPPED TO and who it was BILLED TO.
--
-- # Why the order needs its own copy
--
-- The cart already holds both, copied from the customer's address book for a
-- stated reason: a shopper who later edits their address book must not rewrite
-- what a past cart said. The cart's own comment even names the order as the
-- thing being protected — "the past cart (and the order born out of it)".
--
-- The order born out of it had no address at all. So an order could not say
-- where it went, an invoice could not print a buyer, a shipping label had no
-- destination, and a B2B buyer's company was nowhere on the record. The cart
-- is DELETED or reused after checkout; leaving the fact there would make the
-- protection the cart comment describes protect nothing.
--
-- # No foreign key to the customer's address
--
-- source_address_id records which address book entry it came from and is NOT a
-- foreign key (Principle 2.2): the customer module owns that row and may delete
-- it, and an order that lost its address because a customer tidied their
-- address book would be an order that cannot be shipped or invoiced.
CREATE TABLE IF NOT EXISTS order_addresses (
    id                TEXT        PRIMARY KEY,
    order_id          TEXT        NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    -- address_type is either 'shipping' or 'billing'.
    address_type      TEXT        NOT NULL,
    -- source_address_id is the identity of the customer address it was copied
    -- from; there is NO FK.
    source_address_id TEXT,
    first_name        TEXT,
    last_name         TEXT,
    company           TEXT,
    address_1         TEXT,
    address_2         TEXT,
    city              TEXT,
    province          TEXT,
    postal_code       TEXT,
    country_code      TEXT,
    phone             TEXT,
    metadata          JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT order_addresses_type_valid
        CHECK (address_type IN ('shipping', 'billing'))
);

-- ONE address of each type per order.
--
-- The cart allows the same, and the reason to enforce it here as well is that
-- an order with two shipping addresses is a parcel with two destinations: a
-- question no code below can answer and no operator can unpick afterwards.
CREATE UNIQUE INDEX IF NOT EXISTS order_addresses_one_per_type
    ON order_addresses (order_id, address_type);
