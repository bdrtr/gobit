-- A device is bound to a customer only by a proof (ADR 0440).
--
-- Until this migration a subscription's customer_id was stored as the
-- storefront wrote it, and anybody holding the publishable key could write any
-- customer's id beside an endpoint of their own; that device then received the
-- customer's order confirmations (D293). From here on the subscribe route binds
-- a device only to the customer the installation's identity proves.
--
-- A binding written before cannot be told from one written by a stranger, so
-- every one leaves customer_id. It is MOVED, not dropped: unproven_customer_id
-- keeps it, and updated_at is left as it was, because the two are what an
-- operator compares with the request log to find a stranger's binding, and an
-- upgrade run before reading the advisory must not erase the evidence. Nothing
-- reads the column to push; erasure deletes by it as by customer_id. The
-- devices stay: each keeps receiving broadcasts, and its customer's
-- confirmations again once its browser subscribes with a proof.
ALTER TABLE webpush_subscription
    ADD COLUMN IF NOT EXISTS unproven_customer_id TEXT NOT NULL DEFAULT '';

UPDATE webpush_subscription
SET unproven_customer_id = customer_id,
    customer_id = ''
WHERE customer_id <> '';
