-- The payment module's four tables lose their deleted_at columns.
--
-- # Why the columns go rather than gaining a writer
--
-- A MONEY RECORD IS KEPT, and the retreat from one is a ROW or a STATUS. The
-- module says both already. payment_sessions.sql opens with "A session record
-- is NEVER DELETED, its status changes", and it is not a preference: the
-- idempotence of the compensation rests on it, because a deleted session and a
-- session that never existed cannot be told apart. refunds.sql records the
-- other half -- a repayment is a new row whose sum lives in
-- payments.refunded_amount, never a capture taken back. And the module already
-- ships a table that says the whole thing out loud: payment_manual_sessions
-- carries NO soft delete, with the reason "its records are never deleted".
--
-- Nothing has ever written any of the four. Every read carried
-- "deleted_at IS NULL" -- 22 of the module's 31 statements -- and the predicate
-- has never once been false in a running shop.
--
-- What is left after the columns is not less than what was there. A collection
-- retires through its own derived status, which already has 'canceled' among
-- the eight; a session moves to 'canceled' or 'failed'; a capture is answered
-- by a refund. Not one of those is a hidden row.
--
-- # The uniqueness rules, and a correction
--
-- fulfillment's 000003 removed a column of this shape and distinguished itself
-- from these ten with the sentence "D9's ten carry no such rule", meaning a
-- uniqueness rule written as "unique among LIVING rows". THAT SENTENCE IS
-- WRONG, and it was measured on 2026-09-08: four of the ten carry exactly that
-- rule, and two of the four are here.
--
-- payment_sessions_provider_idempotency_uniq is the last defence that stops two
-- concurrent opens from writing two sessions, and payments_session_uniq is what
-- makes Capture idempotent -- the schema saying at most one capture comes out
-- of a session. While the column stood, one hand-written UPDATE reopened both:
-- measured on a real PostgreSQL 16.14, stamping the first row let the same key
-- write a second live one. The unconditional indexes below refuse that state.
--
-- # The indexes have to be rebuilt, and the drop is silent
--
-- PostgreSQL drops any index whose PREDICATE names a dropped column, with no
-- notice. Eight of this module's nine indexes are partial on deleted_at.
-- Measured on the repository's own PostgreSQL 16.14 before this file was
-- written: DROP COLUMN removed a UNIQUE partial index without a word, and the
-- duplicate key that had been impossible one statement earlier was accepted.
--
-- If an installation HAS stamped the column by hand, this migration fails on a
-- unique index rather than dropping a guarantee quietly. On this table that is
-- the whole point: the index it would drop is the one standing between a retry
-- and a SECOND CHARGE.
ALTER TABLE payment_collections DROP COLUMN IF EXISTS deleted_at;
ALTER TABLE payment_sessions    DROP COLUMN IF EXISTS deleted_at;
ALTER TABLE payments            DROP COLUMN IF EXISTS deleted_at;
ALTER TABLE refunds             DROP COLUMN IF EXISTS deleted_at;

CREATE INDEX IF NOT EXISTS payment_collections_reference_idx
    ON payment_collections (reference);

-- Renamed from payment_collections_alive_idx. The old name described the
-- PREDICATE that no longer exists, and "alive" on an index over every row is
-- the next reader's wrong assumption.
CREATE INDEX IF NOT EXISTS payment_collections_listing_idx
    ON payment_collections (created_at DESC, id DESC);

-- A (provider, idempotency key) pair is now unique among ALL sessions rather
-- than among living ones, which is what the core/provider idempotency
-- requirement always meant: there is no way to stop being a session.
CREATE UNIQUE INDEX IF NOT EXISTS payment_sessions_provider_idempotency_uniq
    ON payment_sessions (provider_id, idempotency_key);

CREATE INDEX IF NOT EXISTS payment_sessions_collection_idx
    ON payment_sessions (payment_collection_id, created_at DESC);

-- AT MOST ONE capture comes out of a session, now without the escape a stamped
-- row used to be.
CREATE UNIQUE INDEX IF NOT EXISTS payments_session_uniq
    ON payments (payment_session_id);

CREATE INDEX IF NOT EXISTS payments_collection_idx
    ON payments (payment_collection_id, created_at DESC);

CREATE INDEX IF NOT EXISTS refunds_payment_idx
    ON refunds (payment_id, created_at DESC);

-- The reconciliation index keeps the half of its predicate that still
-- discriminates. 000002's argument is unchanged and is now shorter by a term:
-- sessions leave the suspect set for good the moment they are captured,
-- canceled or declined, so the index is still the small live-authorized
-- fraction of the table and updated_at is still the last column, sorted the way
-- ListSessionsForReconciliation reads it.
CREATE INDEX IF NOT EXISTS payment_sessions_reconcile_idx
    ON payment_sessions (updated_at)
    WHERE status = 'authorized';
