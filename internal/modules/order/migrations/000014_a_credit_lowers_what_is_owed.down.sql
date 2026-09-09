-- The table goes and takes the concessions with it. Nothing else in the module
-- reads them, so a rolled-back schema reports every order's outstanding amount
-- as total - (paid - refunded) again -- the state before this migration, and a
-- LARGER amount for any order that had been credited.
DROP INDEX IF EXISTS order_credit_lines_order_idx;
DROP TABLE IF EXISTS order_credit_lines;
