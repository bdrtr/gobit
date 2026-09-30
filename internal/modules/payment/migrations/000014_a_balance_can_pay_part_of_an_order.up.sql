-- A balance can pay part of an order (ADR 0269).
--
-- A session of store credit or points holds the whole amount or declines,
-- unless whoever opened it asked for a partial hold: then a balance smaller
-- than the session holds what it has and leaves the rest of the collection to
-- another session. The request is the session's, not the tender's, so a balance
-- paying alone still declines when it falls short. A gift card holds part of
-- every session it opens and needs no column (ADR 0209).
ALTER TABLE payment_store_credit_sessions
    ADD COLUMN IF NOT EXISTS partial BOOLEAN NOT NULL DEFAULT false;

ALTER TABLE payment_loyalty_sessions
    ADD COLUMN IF NOT EXISTS partial BOOLEAN NOT NULL DEFAULT false;
