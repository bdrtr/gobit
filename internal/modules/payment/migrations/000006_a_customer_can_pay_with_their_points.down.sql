-- Rolling back takes the tender's sessions with it and narrows the ledger's
-- vocabulary to the two earning kinds.
--
-- The narrowed CHECK is validated against the rows that exist, so on a ledger
-- holding a spend row this rollback STOPS rather than deleting it — which is
-- fulfillment's 000004 decision and the module's own rule since 000003: a
-- rollback that stops is recoverable, a rollback that rewrote a money record is
-- not.
DROP TABLE IF EXISTS payment_loyalty_sessions;

ALTER TABLE payment_loyalty_entries DROP CONSTRAINT IF EXISTS payment_loyalty_entries_sign_matches_kind;
ALTER TABLE payment_loyalty_entries ADD CONSTRAINT payment_loyalty_entries_sign_matches_kind
    CHECK ((kind = 'reverse' AND points < 0) OR (kind = 'earn' AND points > 0));

ALTER TABLE payment_loyalty_entries DROP CONSTRAINT IF EXISTS payment_loyalty_entries_kind_valid;
ALTER TABLE payment_loyalty_entries ADD CONSTRAINT payment_loyalty_entries_kind_valid
    CHECK (kind IN ('earn', 'reverse'));
