-- order_search_document is what the admin list's q is matched against
-- (ADR 0456): the words of the fields named, folded and split, and every run
-- of four or more digits that ends the phone number.
--
-- # The fold is gobit's, not the cluster's
--
-- translate() replaces each Latin letter carrying a mark, small or capital,
-- with its ASCII base, and lower() then folds what is left. The two strings are
-- internal/core/textfold's table rendered by SQLTranslate, and
-- TestEverySearchFoldIsTheGoFold in internal/arch holds this file to them, so
-- a Turkish capital folds on a cluster created with --locale=C as well. A
-- letter outside the table folds as lower() folds it there (ADR 0015).
--
-- # The words are split at every ASCII sign
--
-- An address's dots and at sign become spaces, so "ayse.yilmaz@example.com"
-- gives the words ayse, yilmaz, example and com, each found by its start. The
-- class names code points rather than [:punct:], which reads the CTYPE.
--
-- # A phone is found by any part of its digits
--
-- Its digits are stored as every suffix of four or more: a run typed with or
-- without the leading 0 or 90 is the start of one of them.
--
-- # Why plpgsql, IMMUTABLE and COST 1000
--
-- The index is on this expression and the query repeats it, so the function
-- must be IMMUTABLE; it reads nothing but its arguments. A SQL function costs
-- about fifteen times as much per row because its plan is not kept between
-- calls (docs/measurements/0456-what-a-search-read.md). COST tells the
-- planner what a row costs, so a plan that would compute the document for
-- every row of the table instead of reading the index is priced as what it
-- is. The body names only pg_catalog, because a restore builds the index
-- with an empty search_path.
CREATE FUNCTION order_search_document(phone text, VARIADIC words text[]) RETURNS tsvector
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

-- order_search_query turns what the operator typed into the query
-- order_search_document is matched by. A q written only in digits and
-- + - ( ) . and spaces, with four digits or more, is a phone number: its digits
-- less a leading 90 or 0 are found as a part of a phone, or as the start of a
-- word. Any other q is its words, folded and split by the same function, each
-- the start of a word. NULL is a q with no word in it, which matches nothing.
--
-- A phone's digit groups are not read as words as well. They would find the
-- same rows through the phone's suffixes, and the planner, reading a q of four
-- short prefixes, expects most of the table and plans for it
-- (docs/measurements/0456-what-a-search-read.md).
--
-- The words come out of to_tsvector with every ASCII sign already taken out,
-- so no quote or backslash reaches the tsquery text.
CREATE FUNCTION order_search_query(q text) RETURNS tsquery
    LANGUAGE plpgsql IMMUTABLE STRICT PARALLEL SAFE AS $$
DECLARE
    digits text;
    terms  text;
BEGIN
    IF q ~ '^[0-9 +().-]+$' THEN
        digits := regexp_replace(q, '[^0-9]', '', 'g');
        IF length(digits) >= 4 THEN
            RETURN ('''' || regexp_replace(digits, '^(90(?=[0-9]{9})|0)', '') || ''':*')::tsquery;
        END IF;
    END IF;
    SELECT string_agg('''' || w || ''':*', ' & ') INTO terms
    FROM unnest(tsvector_to_array(order_search_document(NULL, q))) AS w;
    RETURN terms::tsquery;
END
$$;

-- An order is found by its e-mail or by the names, company and phone of an
-- address it holds now; an address a correction closed is not searched. The
-- two indexes are read apart and their hits united
-- (queries/orders_search.sql). An erased order's columns are NULL, so it
-- leaves both by the erasure's own UPDATE.
CREATE INDEX orders_search_idx ON orders
    USING gin (order_search_document(NULL, email));

CREATE INDEX order_addresses_search_idx ON order_addresses
    USING gin (order_search_document(phone, first_name, last_name, company))
    WHERE superseded_at IS NULL;
