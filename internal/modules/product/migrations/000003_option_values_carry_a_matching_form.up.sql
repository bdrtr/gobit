-- value_folded is the form an option value is MATCHED by, and it is folded by GO.
--
-- # What this is for
--
-- A shopper filtering by "Color: red" has to be told which products have it, and
-- the question A18 asks is what counts as a match: the text exactly as the
-- catalog stores it, the same text case-insensitively, or the text folded. The
-- answer is the third, and this column is where the folded form lives.
--
-- # Why a COLUMN and not an expression index
--
-- The same reason the invoice module's buyer address is a column (ADR 0038): an
-- expression index would have to be written with lower(), and lower() is the
-- CLUSTER's fold. On a database created with --locale=C it folds ASCII and
-- nothing else, so the filter would silently stop matching the very values this
-- decision exists to match, and it would do so on some installations and not
-- others. The fold is Go's, and what the database stores is its result.
--
-- # What the fold does, and the ONE thing it deliberately does not do
--
-- models.FoldOptionValue trims, lower-cases with Go's Unicode rules, and replaces
-- each Latin letter carrying a mark with its ASCII base. Everything else is left
-- exactly as it stands.
--
-- That last clause is the decision. The obvious rule was to reuse slugify, the
-- fold this repository already applies to handles. Measured before this was
-- built, slugify DROPS any rune it cannot transliterate: every Cyrillic value in
-- a catalog folds to the EMPTY STRING, as does every CJK one. On such a catalog
-- the unique index below would not merely mis-match, it would REFUSE TO BUILD,
-- because every value in an option would collide with every other. The rule looks
-- flawless on Turkish, which is why it read as safe.
--
-- # The unique index is per option and it is the constraint that can FAIL
--
-- product_option_value_uniq already forbids two identical values in one option.
-- This adds the same rule for the folded form, which is stricter: a merchant who
-- today has both "Kirmizi" and the dotted spelling of it in one option has two
-- rows that fold to one value, and this migration will refuse to apply until one
-- of them is removed. That refusal is the point — those two rows are the same
-- color typed twice, and a filter cannot tell a shopper which one they meant.
--
-- # The backfill is CORRECT FOR ASCII AND ONLY FOR ASCII
--
-- It is written in SQL because a migration is SQL, and lower() is exactly the
-- fold that cannot be trusted here — so the rows this backfill gets wrong are the
-- rows the decision is about. The convergence is done from Go at startup, the way
-- invoice 000003's is: see [app.refoldOptionValues]. An installation whose option
-- values are pure ASCII needs it for nothing.
ALTER TABLE product_option_value
    ADD COLUMN IF NOT EXISTS value_folded text NOT NULL DEFAULT '';

UPDATE product_option_value
SET value_folded = lower(btrim(value))
WHERE value <> '';

-- Dropped so the INSERT is forced to name the column, which is what keeps it
-- inside TestEveryColumnIsWrittenBySomething's scope; a column the database
-- supplies is out of that audit by its own rule.
ALTER TABLE product_option_value
    ALTER COLUMN value_folded DROP DEFAULT;

CREATE UNIQUE INDEX IF NOT EXISTS product_option_value_folded_uniq
    ON product_option_value (option_id, value_folded) WHERE deleted_at IS NULL;
