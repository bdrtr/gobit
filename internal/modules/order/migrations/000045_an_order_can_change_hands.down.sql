-- Rolling back drops every transfer an order recorded: a request still
-- waiting can no longer be accepted, and the orders keep the customer and the
-- address the last accepted transfer gave them, with nothing saying who held
-- them before.
DROP TABLE IF EXISTS order_transfers;
