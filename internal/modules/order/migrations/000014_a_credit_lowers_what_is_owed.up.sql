-- order_credit_lines records an amount that lowers what an order OWES, without
-- changing what was SOLD.
--
-- # Why the order's total is not lowered instead
--
-- `orders.total` is the cart's snapshot: what was sold and at what price. It is
-- pinned to its lines by orders_totals_consistent and to its own subtotal by
-- orders_discount_within_subtotal, and 000001 calls the order "the permanent
-- answer to the question what was sold at that moment". A goodwill gesture, a
-- price match or a compensation agreed AFTER the sale is none of those things:
-- it changes what the customer has left to pay, not what they bought. Lowering
-- the total would make the order disagree with its own lines and would erase the
-- fact that a concession was made at all.
--
-- # Why the running total is NOT a column on order_summaries
--
-- 000001 answered the same question for the outstanding amount and its reason
-- holds here: "had it been stored, the consistency of the columns with one
-- another would have to be protected by a separate constraint, and a derived
-- value could go stale". The credit total is the SUM of these rows and is read
-- as one.
--
-- # Why the amount is strictly positive
--
-- A negative credit is a CHARGE, and charging a customer more after the sale is
-- a different verb with a different authorization: it would have to reach the
-- payment module, not this table. One column that can mean either would make
-- "credit" the word for both.
CREATE TABLE IF NOT EXISTS order_credit_lines (
    id         TEXT        PRIMARY KEY,
    order_id   TEXT        NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    amount     BIGINT      NOT NULL,
    -- reason is the merchant's short word for WHY, chosen from their own
    -- vocabulary; this module does not enumerate it. What it refuses is an
    -- unexplained concession.
    reason     TEXT        NOT NULL,
    note       TEXT        NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT order_credit_lines_amount_positive CHECK (amount > 0),
    CONSTRAINT order_credit_lines_reason_present  CHECK (reason <> '')
);

-- The listing is per order and in the order the concessions were made.
CREATE INDEX IF NOT EXISTS order_credit_lines_order_idx
    ON order_credit_lines (order_id, created_at, id);
