-- The search document folds as ADR 0456 wrote it, the ASCII capital I left to
-- lower(), and the index is rebuilt by that body.
CREATE OR REPLACE FUNCTION review_search_document(phone text, VARIADIC words text[]) RETURNS tsvector
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
                   U&'\00C0\00C1\00C2\00C3\00C4\00C5\00C7\00C8\00C9\00CA\00CB\00CC\00CD\00CE\00CF\00D1\00D2\00D3\00D4\00D5\00D6\00D9\00DA\00DB\00DC\00DD\00E0\00E1\00E2\00E3\00E4\00E5\00E7\00E8\00E9\00EA\00EB\00EC\00ED\00EE\00EF\00F1\00F2\00F3\00F4\00F5\00F6\00F9\00FA\00FB\00FC\00FD\00FF\011E\011F\0130\0131\015E\015F\0178',
                   'aaaaaaceeeeiiiinooooouuuuyaaaaaaceeeeiiiinooooouuuuyyggiissy')),
               '[\x01-\x2F\x3A-\x40\x5B-\x60\x7B-\x7F]+', ' ', 'g')))
        || array_to_tsvector(suffixes);
END
$$;

REINDEX INDEX reviews_search_idx;
