-- order_search_document translates the ASCII capital I to i before lower()
-- sees it (ADR 0460). lower() folds every other ASCII letter alike on every
-- cluster, but a Turkish or Azerbaijani CTYPE lowers I to the dotless i
-- (U+0131), which translate() has already passed by: a name stored as WILLIS
-- was indexed with a dotless i, and a q typed any other way folds to i and
-- found nothing. The two strings are internal/core/textfold's table rendered
-- by SQLTranslate, I first among them, and TestEverySearchFoldIsTheGoFold
-- holds the module's latest definition to them. The body is otherwise ADR
-- 0456's; the query function folds by this one and is unchanged.
--
-- The index on the expression is rebuilt, since its entries were computed by
-- the old body; REINDEX holds off writes to the table while it runs, as the
-- index's first build did.
CREATE OR REPLACE FUNCTION order_search_document(phone text, VARIADIC words text[]) RETURNS tsvector
    LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE COST 1000 AS $$
DECLARE
    digits   text   := regexp_replace(coalesce(phone, ''), '[^0-9]', '', 'g');
    suffixes text[] := '{}';
BEGIN
    FOR i IN 1 .. length(digits) - 3 LOOP
        suffixes := suffixes || substr(digits, i);
    END LOOP;
    RETURN strip(to_tsvector('simple'::regconfig, regexp_replace(
               lower(translate(coalesce(array_to_string(words, ' '), ''),
                   U&'\0049\00C0\00C1\00C2\00C3\00C4\00C5\00C7\00C8\00C9\00CA\00CB\00CC\00CD\00CE\00CF\00D1\00D2\00D3\00D4\00D5\00D6\00D9\00DA\00DB\00DC\00DD\00E0\00E1\00E2\00E3\00E4\00E5\00E7\00E8\00E9\00EA\00EB\00EC\00ED\00EE\00EF\00F1\00F2\00F3\00F4\00F5\00F6\00F9\00FA\00FB\00FC\00FD\00FF\011E\011F\0130\0131\015E\015F\0178',
                   'iaaaaaaceeeeiiiinooooouuuuyaaaaaaceeeeiiiinooooouuuuyyggiissy')),
               '[\x01-\x2F\x3A-\x40\x5B-\x60\x7B-\x7F]+', ' ', 'g')))
        || array_to_tsvector(suffixes);
END
$$;

REINDEX INDEX orders_search_idx;
REINDEX INDEX order_addresses_search_idx;
