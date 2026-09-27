-- A receiver can narrow what it gets (ADR 0218).
--
-- filters holds, per topic, the payload fields an event must carry one of the
-- listed values in: {"cart.created": {"region_id": ["reg_1"]}}. Every field
-- named must match, and an event that does not is written to no delivery of
-- this receiver. fields holds, per topic, the payload fields the receiver is
-- sent: {"order.placed": ["order_id", "total"]}; a topic it does not name is
-- sent whole. Both are checked against the forwarded topics' fields when they
-- are written, and both are empty for a receiver that narrows nothing.
ALTER TABLE webhook_endpoint
    ADD COLUMN IF NOT EXISTS filters jsonb NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN IF NOT EXISTS fields  jsonb NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE webhook_endpoint
    ADD CONSTRAINT webhook_endpoint_filters_object CHECK (jsonb_typeof(filters) = 'object'),
    ADD CONSTRAINT webhook_endpoint_fields_object  CHECK (jsonb_typeof(fields) = 'object');
