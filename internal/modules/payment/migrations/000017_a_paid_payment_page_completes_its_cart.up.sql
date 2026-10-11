-- A paid payment page completes its cart, and one that cannot is asked about
-- (ADR 0438).
--
-- A provider whose customer pays on the provider's own page (PayTR) has its
-- session opened before the cart is completed, and the session stays pending
-- here until a completion authorizes it. A customer who pays and closes the
-- browser leaves exactly that: money taken, a pending session, no order. A
-- customer who pays on a page whose session was canceled meanwhile leaves the
-- same money on a canceled session. Reconciliation asks those providers about
-- their pending and canceled sessions, newest first, and this is the index
-- that read walks backwards.
--
-- It is PARTIAL on the status for 000002's reason: a session leaves the set
-- the moment it is authorized, captured or declined, which is most sessions,
-- and the pending sessions of a driven provider live only inside one
-- checkout. A canceled session stays in it for good, every provider's: the
-- read needs a day of them and the index keeps one entry per cancel, the
-- price of not naming a provider in a migration.
CREATE INDEX IF NOT EXISTS payment_sessions_pending_reconcile_idx
    ON payment_sessions (updated_at)
    WHERE status IN ('pending', 'canceled');

-- The reports a provider whose customer pays on its own page made about its
-- sessions, and what the checkout did with each (ADR 0438).
--
-- PayTR posts the outcome of a payment once, and the shopper who paid may
-- close the browser before the storefront completes the cart, so the report
-- itself completes it on the server. A completion that fails — stock gone, a
-- fault on the way — is tried again with a growing wait, and this row is what
-- remembers the attempts across processes: attempts, when the next one is
-- due, the last failure, and the outcome once there is one. A row is never
-- deleted; its outcome says how the payment ended: placed an order, given back
-- because the cart no longer matched, closed by someone else, left to a
-- person, or given up after the last attempt.
CREATE TABLE IF NOT EXISTS payment_session_reports (
    session_id      text        PRIMARY KEY REFERENCES payment_sessions (id),
    reported_at     timestamptz NOT NULL DEFAULT now(),
    attempts        integer     NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL,
    last_failure    text        NOT NULL DEFAULT '',
    outcome         text        NOT NULL DEFAULT '',
    settled_at      timestamptz,
    CONSTRAINT payment_session_reports_attempts_not_negative CHECK (attempts >= 0),
    CONSTRAINT payment_session_reports_outcome_known
        CHECK (outcome IN ('', 'completed', 'refunded', 'closed', 'left', 'gave_up')),
    CONSTRAINT payment_session_reports_settled_with_outcome
        CHECK ((outcome = '') = (settled_at IS NULL))
);

-- The sweep reads the reports still open whose next attempt is due, soonest
-- first; a settled report is never read again.
CREATE INDEX IF NOT EXISTS payment_session_reports_due_idx
    ON payment_session_reports (next_attempt_at)
    WHERE outcome = '';

-- The secret a cart's opened payment hands its storefront, kept as a digest,
-- and when it was spent (ADR 0438, ADR 0439).
--
-- A provider's report may complete the cart before the storefront asks, and a
-- completed cart answers the storefront the unknown cart's 404. The secret is
-- what answers the storefront's completion of that payment, once, with the
-- order and its access token: an id read off a log or a page opens nothing,
-- and this is not an id. The digest is SHA-256 of 256 random bits, so it needs
-- no slow hash; the plaintext is in the opening's answer alone.
CREATE TABLE IF NOT EXISTS payment_session_answers (
    session_id    text        PRIMARY KEY REFERENCES payment_sessions (id),
    secret_digest bytea       NOT NULL,
    answered_at   timestamptz,
    CONSTRAINT payment_session_answers_digest_width CHECK (octet_length(secret_digest) = 32)
);
