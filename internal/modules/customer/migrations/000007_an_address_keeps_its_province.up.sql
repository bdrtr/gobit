-- A saved address keeps its province (ADR 0369): the sub-country unit under
-- the country, an il in Turkey, as the cart's and the order's addresses mean
-- it (ADR 0067), so a shopper who checks out from their address book brings it
-- to the cart. Empty where the customer never gave one, as the address's other
-- optional fields are.
ALTER TABLE customer_address
    ADD COLUMN IF NOT EXISTS province TEXT NOT NULL DEFAULT '';
