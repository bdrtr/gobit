-- The reverse of 000008. The claims go with the table: an order placed after the
-- upgrade is left as one placed before it, owed nothing (ADR 0392).
DROP TABLE IF EXISTS inventory_backorders;
