-- The table goes with its index; the lines it hangs from keep their own rate,
-- which is the stack's base, so a rolled-back schema still totals every order
-- correctly and prints one rate per line -- the state before this migration.
DROP INDEX IF EXISTS order_line_taxes_position_uniq;
DROP TABLE IF EXISTS order_line_taxes;
