-- Both tables go together: the items cannot outlive the replacement they hang
-- from, and nothing else in the schema points at either.
DROP TABLE IF EXISTS order_replacement_items;
DROP TABLE IF EXISTS order_replacements;
