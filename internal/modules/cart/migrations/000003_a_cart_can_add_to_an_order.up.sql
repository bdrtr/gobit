-- A cart can be opened to add to an order (ADR 0192).
--
-- The order it names is the order module's; the column carries its id as free
-- text and no foreign key (Principle 2.2). It is written when the cart is opened
-- and never changed, and the checkout carries it into the order the cart
-- becomes, where the order module checks it under a lock on the parent. The
-- cart module does not read orders; the workflow that opens the cart asked the
-- order module first.
ALTER TABLE carts
    ADD COLUMN IF NOT EXISTS adds_to_order_id TEXT;

ALTER TABLE carts
    ADD CONSTRAINT carts_adds_to_order_not_blank
        CHECK (adds_to_order_id IS NULL OR length(btrim(adds_to_order_id)) > 0);
