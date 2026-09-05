-- The exchange record stops promising a completion this framework cannot
-- perform, and starts recording the one transition it can.
--
-- # What was measured
--
-- order_exchanges carried completed_at and canceled_at from the day the table
-- was created and NOTHING has ever written either: the module has no UPDATE
-- against this table at all, so status is written once by the INSERT (always
-- 'requested') and never again. Both stamps were therefore NULL on every row
-- that has ever existed, and 'completed' and 'canceled' were values the CHECK
-- allowed and no code path could produce.
--
-- The absence is not an oversight, and the reason is recorded in two places in
-- the source. internal/workflows/returns/claim.go refuses to settle a claim
-- with a replacement because it "needs goods shipped out against an existing
-- order — there is no capability for that anywhere in the framework". And the
-- link definition in internal/modules/payment/service/links.go names this exact
-- record as the thing that will reopen its cardinality one day: the order to
-- payment link is "ONE TO ONE, which is the strictest constraint that is true
-- today ... The nearest candidate is already visible in the schema: an exchange
-- whose difference_due is positive is money collected against an existing
-- order. That day this becomes OneToMany and nothing else changes."
--
-- Completing an exchange needs BOTH of those — goods out and, when the
-- difference is positive, money in — and the framework has neither. So the
-- column is dropped rather than kept as a field the API publishes and no
-- transition can ever fill.
--
-- # Why 'completed' leaves the CHECK too
--
-- Keeping the status value while dropping its stamp would leave a reachable-
-- looking state with nothing to reach it and no moment to date it. The
-- vocabulary and the schema have to say the same thing, and today both say: an
-- exchange is opened, and it is either still open or abandoned.
--
-- If a row somewhere carries status = 'completed', this migration FAILS rather
-- than rewriting it. That is the wanted outcome: no code in this repository
-- could have written that row, so it was written by hand, and quietly setting
-- it back to 'requested' would destroy the only evidence of whatever was done
-- outside the system.
--
-- # Why canceled_at stays and gains a mirror CHECK
--
-- Abandoning an exchange needs none of the missing capabilities. Nothing ships,
-- nothing is collected, no other module is touched: a request was opened and it
-- is withdrawn. CancelOrderExchange writes it, so the column is a fact from
-- here on rather than a promise.
--
-- The mirror form — status and stamp implying each other — is normally not
-- addable to a column that already exists, because rows written before the
-- transition existed would carry the status and no moment. Here it IS addable,
-- and precisely BECAUSE the column was dead: no row can hold 'canceled', so
-- every existing row satisfies both directions. This is the constraint
-- orders_canceled_stamp has on the order, and 000007 had to give up for
-- archived_at.
--
-- # Restoring completion later
--
-- It is a migration, and a cheap one: add the column back, widen the CHECK, add
-- the transition. Nothing is lost by dropping it now, because a column that was
-- never written holds no data to lose — which is the same fact that made the
-- promise empty in the first place.
ALTER TABLE order_exchanges
    DROP COLUMN IF EXISTS completed_at;

ALTER TABLE order_exchanges
    DROP CONSTRAINT IF EXISTS order_exchanges_status_valid;

ALTER TABLE order_exchanges
    ADD CONSTRAINT order_exchanges_status_valid
        CHECK (status IN ('requested', 'canceled'));

ALTER TABLE order_exchanges
    ADD CONSTRAINT order_exchanges_canceled_stamp
        CHECK ((status = 'canceled') = (canceled_at IS NOT NULL));
