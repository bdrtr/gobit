-- cart_promotion_code holds the coupon codes the shopper typed into the cart.
--
-- # Why the cart stores it at all
--
-- Until this table existed only AUTOMATIC promotions could reach a cart. The
-- discount request had a "codes" array and the cart could not fill it: there was
-- nowhere to put a code, so the totals a customer saw were reproducible from the
-- cart's own state precisely BECAUSE the coupon path did not exist. The cart
-- workflow's package comment named the three points a coupon would be wired into
-- and this is the first of them.
--
-- Passing the code to the totals call instead was the rejected alternative, and
-- the reason is written in that comment: the flow is entered from three places,
-- and a code given to only one of them would make the discount appear and
-- disappear depending on which entry point ran last. A quantity raised by one
-- would silently drop the coupon.
--
-- # Why the CODE and not the promotion's id
--
-- The code is what the customer typed and what they will read back on the
-- checkout page. Resolving it to a promotion id at write time would freeze a
-- decision that the discount round makes fresh every time: a promotion that is
-- paused, whose campaign closes, or whose budget runs out simply stops
-- discounting, and the code sitting in the cart stops mattering by itself. An id
-- would still point at it and would have to be cleaned up by something.
--
-- # Why a table and not an array column
--
-- A cart can carry several codes, and each one is a row a UNIQUE constraint can
-- speak about: typing the same code twice is a double press rather than a second
-- coupon. An array column would have to be de-duplicated in Go, in every writer.
--
-- # Why there is NO deleted_at
--
-- Every other child of a cart is soft deleted, and this one is not. The row is a
-- BINDING and not a record of something that happened: removing a coupon the
-- shopper decided against should leave nothing behind, and nothing in this
-- module or any other reads a coupon that was once applied. What the shop wants
-- to know — which promotion was actually spent — is `promotion_redemption`, and
-- that record is written at order time.
CREATE TABLE IF NOT EXISTS cart_promotion_code (
    cart_id    TEXT        NOT NULL REFERENCES carts (id) ON DELETE CASCADE,
    -- code is stored in UPPER case; the promotion module normalizes the same
    -- way, so a cart holding "summer20" would be asking about a code that side
    -- cannot match.
    code       TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (cart_id, code),
    CONSTRAINT cart_promotion_code_present CHECK (code <> ''),
    CONSTRAINT cart_promotion_code_upper CHECK (code = upper(code))
);

-- The listing is per cart and in the order the codes were typed.
CREATE INDEX IF NOT EXISTS cart_promotion_code_cart_idx
    ON cart_promotion_code (cart_id, created_at, code);

