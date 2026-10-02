-- A review says whether its writer bought the product (ADR 0372).
--
-- 000001 left "verified purchase" out of this table because an order id in a
-- request body proves only that the writer holds one, and ADR 0008 then left
-- customer identity to the embedder. Since ADR 0043 a storefront request can
-- PROVE a customer, and the badge is computed from that customer's own orders
-- when the review is written; nothing a body carries decides it.
--
-- The customer is asked about and NOT stored. A badge needs the customer only
-- at the moment of writing, and storing them would turn every review into a
-- person's record — an erasure and a disclosure to answer — for nothing the
-- badge uses afterwards. author_name stays the only thing held about a writer.
ALTER TABLE reviews
    ADD COLUMN IF NOT EXISTS verified_purchase boolean NOT NULL DEFAULT false;
