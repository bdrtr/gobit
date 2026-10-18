DROP INDEX IF EXISTS reviews_search_idx;
DROP FUNCTION IF EXISTS review_search_query(text);
DROP FUNCTION IF EXISTS review_search_document(text, text[]);
