-- Rolling back the record of pushed events (ADR 0389).
--
-- A delivery of an order placed within the last four hours pushes once more
-- after this: the record of what was pushed is gone. webpush_subscription is
-- untouched, so no device has to subscribe again.
DROP INDEX IF EXISTS webpush_claimed_event_claimed_at_idx;
DROP TABLE IF EXISTS webpush_claimed_event;
