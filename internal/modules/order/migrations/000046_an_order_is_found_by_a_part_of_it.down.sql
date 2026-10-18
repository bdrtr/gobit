DROP INDEX IF EXISTS order_addresses_search_idx;
DROP INDEX IF EXISTS orders_search_idx;
DROP FUNCTION IF EXISTS order_search_query(text);
DROP FUNCTION IF EXISTS order_search_document(text, text[]);
