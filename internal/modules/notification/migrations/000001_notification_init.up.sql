-- Schema of the notification module (plan Section 5.6).
--
-- Ownership: this table belongs ONLY to the notification module. reference is
-- an order identifier but IT IS NOT A FOREIGN KEY (Principle 2.2 — the
-- cross-module FK ban): the order is owned by the order module and the relation
-- is established over Module Links when it is needed.
--
-- Time: every stamp is timestamptz (UTC).

-- notification_deliveries IS THE DELIVERY LOG: it holds which template, over
-- which channel, for which reference and with which provider a send was
-- attempted, and its outcome.
--
-- # THE RECIPIENT ADDRESS IS NOT STORED
--
-- There is neither an email nor a phone number in the table, and this is
-- deliberate. The address already sits on the order record; a second copy
-- raises the number of places that have to be cleaned up on a KVKK/GDPR erasure
-- request, and forgetting that copy means the data of a person believed erased
-- stays in the system. The question the log has to answer is not "who did it go
-- to" but "did it go out"; the reference is enough to bind the record to the
-- order.
--
-- # (template, reference) IS UNIQUE
--
-- This is the only thing idempotency rests on. The event bus does not redeliver
-- today (see core/eventbus), but an event may be republished by hand or a
-- subscription may be set up twice; the uniqueness closes that door. The record
-- is written BEFORE the send (status = 'pending'), that is, only one of two
-- concurrent handlers goes to the provider.
--
-- # THERE IS NO SOFT DELETION
--
-- Unlike the other modules there is no deleted_at column. There are two reasons:
-- the record carries no personal data, that is, it contains nothing that has to
-- be erased; and a soft-deleted row would go on OCCUPYING the uniqueness key —
-- a log record that looks deleted would mean the same notification could never
-- be sent again.
CREATE TABLE IF NOT EXISTS notification_deliveries (
    id          TEXT        PRIMARY KEY,
    -- template is the template of the notification sent; it is chosen the same
    -- as the event name (e.g. "order.placed").
    template    TEXT        NOT NULL,
    -- channel is the send channel ("email" | "sms").
    --
    -- A CHECK constraint IS DELIBERATELY ABSENT: the list of channels is
    -- defined in the core (internal/core/provider) and plugins can bring new
    -- channels. Pinning the values here would have made writing a migration
    -- mandatory for every channel added to the core; whereas the log records
    -- the send that was ATTEMPTED — whether the channel is supported is decided
    -- by the provider.
    channel     TEXT        NOT NULL,
    -- reference is the identifier of the record the notification is bound to
    -- (the order). THERE IS NO FK (Principle 2.2).
    reference   TEXT        NOT NULL,
    -- provider_id is the identity of the provider that performed the send. It
    -- is stored because the configuration changes: which provider was tried a
    -- month ago can only be known if it is written on the record.
    provider_id TEXT        NOT NULL,
    status      TEXT        NOT NULL DEFAULT 'pending',
    -- error is filled only while status = 'failed'; it is for diagnosis.
    error       TEXT        NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT notification_deliveries_template_not_empty  CHECK (template <> ''),
    CONSTRAINT notification_deliveries_reference_not_empty CHECK (reference <> ''),
    CONSTRAINT notification_deliveries_channel_not_empty   CHECK (channel <> ''),
    -- A row left at 'pending' means the outcome could not be written after the
    -- provider was reached; that is why it has to be in the status list — that
    -- row is the proof of a fault and must stay visible.
    CONSTRAINT notification_deliveries_status_valid
        CHECK (status IN ('pending', 'sent', 'failed', 'skipped'))
);

-- A SECOND record cannot be opened for the same (template, reference); this is
-- the constraint that ultimately stops a duplicate notification.
CREATE UNIQUE INDEX IF NOT EXISTS notification_deliveries_template_reference_uniq
    ON notification_deliveries (template, reference);

-- The admin listing is paged from the newest to the oldest; the index serves
-- that ordering.
CREATE INDEX IF NOT EXISTS notification_deliveries_recent_idx
    ON notification_deliveries (created_at DESC, id DESC);

-- Looking up the notifications of an order is the log's most frequent question
-- ("did the confirmation go to the customer").
CREATE INDEX IF NOT EXISTS notification_deliveries_reference_idx
    ON notification_deliveries (reference);
