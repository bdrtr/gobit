-- The order journal reads the exchanges funded in a window (ADR 0203), as it
-- reads the orders placed and canceled in one (000021).
CREATE INDEX IF NOT EXISTS order_exchanges_funded_idx
    ON order_exchanges (funded_at, id)
    WHERE funded_at IS NOT NULL;
