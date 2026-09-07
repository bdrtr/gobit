-- buyer_email_folded is the handle an erasure resolves a person by, and it is
-- folded by GO rather than by the cluster.
--
-- # What was wrong, measured rather than argued
--
-- 000002 added invoices_buyer_email_idx ON invoices (lower(buyer_email)) and
-- the erasure counted with "WHERE lower(buyer_email) = lower($1::text)". The
-- reasoning written there is sound as far as it goes -- this table has no
-- customer_id and no order_id, the address is the only handle on a person, and
-- a case-sensitive match "would let one capital letter answer 0 rows retained
-- about a person whose invoice is sitting in the table". What it assumed is
-- that lower() folds letters. It folds the ones the CLUSTER's ctype knows.
--
-- Measured 2026-09-07 against the development database, which reports
-- datcollate=C, datctype=C, datlocprovider=c:
--
--   SELECT lower('ALI@X.COM');   -- ali@x.com
--   SELECT lower('O-with-diaeresis');  -- unchanged
--   SELECT ('C-cedilla' ILIKE 'c-cedilla');  -- false
--
-- Reproduced with this module's own predicate on that cluster: a row holding
-- the address as the document recorded it, matched against the folded form
-- every OTHER holder of that person's data stores, counted 0. Matched against
-- the document's own casing it counted 1. The ASCII control counted 1.
--
-- Zero is not a quiet branch in this module. Service.Erase returns whyNothingHere
-- for it, whose entire purpose is to distinguish "nobody looked" from "we looked
-- and you are not here", and the second sentence is the one a controller repeats
-- to a data subject. On that cluster it was false while the document was held.
--
-- # Why a COLUMN and not a better expression
--
-- Three candidates were weighed and two were refused.
--
-- Fold the SUBJECT harder in Go before the query. It does not help: the side
-- the cluster fails to fold is the STORED one, and no amount of folding the
-- argument makes lower(buyer_email) fold.
--
-- Use an explicit collation, lower(buyer_email COLLATE "und-x-icu"). It trades
-- a dependency on the cluster's ctype for a dependency on ICU being present,
-- which is the same KIND of dependency this is trying to escape. It also still
-- would not agree with the five Go copies: Go maps the Turkish dotted capital I
-- to a single rune and ICU's full case mapping produces two, so the one letter
-- this was found on would still divide the holders.
--
-- Write the fold from Go into its own column. The fold is then the SAME
-- expression that folds an address in auth, customer, b2b, order and cart --
-- one Go function, compared against the other five by an audit in internal/arch
-- -- and it does not consult the cluster at all. That is this migration.
--
-- # What is NOT folded
--
-- buyer_email keeps what the document said. An invoice is a snapshot and
-- ADR 0024 makes it immutable; rewriting the printed address to make a query
-- cheaper would edit a legal document. seller_email is untouched for a
-- different reason: the seller is not a data subject of this framework's
-- erasure, and module.go already states that an erasure naming the seller's
-- own address counts zero invoices.
--
-- # Why there is no CHECK tying the two columns together
--
-- The mirror this repository reaches for -- CHECK (buyer_email_folded =
-- lower(btrim(buyer_email))) -- is written in the very expression that is being
-- escaped. On a C-locale cluster it would REJECT exactly the rows this
-- migration exists to store correctly: a Go-folded non-ASCII address is not
-- what lower() produces there, so the constraint would refuse the fix and
-- accept the defect. A constraint that can only be satisfied by being wrong is
-- worse than none, and this is the second time in this table's history that
-- lower() has looked like it stated a rule when it stated a locale.
--
-- What holds the pairing instead is the INSERT, which names the column, plus
-- the column audit that requires it to (see the DROP DEFAULT below).
--
-- # The DEFAULT is added and then DROPPED, in that order and for two reasons
--
-- The column is NOT NULL on a table that already has rows, so it needs a
-- default to be added at all. Keeping it would then put the column OUT of the
-- scope of TestEveryColumnIsWrittenBySomething, whose rule is that a column the
-- database supplies is not the statement's business -- which is exactly the
-- silence 000009 of the order module warns about in its own header: "the day
-- the statement stopped naming this column, nothing would fail". Dropping the
-- default puts the column back inside that audit and makes an INSERT that
-- forgets it fail loudly instead of storing an empty handle.
--
-- # The backfill below is CORRECT FOR ASCII AND ONLY FOR ASCII
--
-- It has to be written in SQL, because a migration is SQL, and SQL is the thing
-- that cannot fold reliably here -- so the backfill reproduces the defect for
-- exactly the rows the defect is about. This is stated rather than hidden: on a
-- cluster that folds only ASCII, a pre-existing row whose buyer address carries
-- a non-ASCII letter gets a folded value that Go would not have produced, and
-- an erasure naming that person still misses it.
--
-- The remedy is a Go pass over those rows and it is a COMMAND, not a hidden
-- step at boot: `gobit refold-invoices`. It re-folds with the same Go function
-- the INSERT uses, it touches only rows whose address is not pure ASCII, and it
-- is idempotent. An installation whose invoices are all ASCII needs it for
-- nothing, which is why it is not run automatically.
ALTER TABLE invoices
    ADD COLUMN IF NOT EXISTS buyer_email_folded text NOT NULL DEFAULT '';

UPDATE invoices
SET buyer_email_folded = lower(btrim(buyer_email))
WHERE buyer_email <> '';

ALTER TABLE invoices
    ALTER COLUMN buyer_email_folded DROP DEFAULT;

-- A plain b-tree on the stored value. 000002's index was on an EXPRESSION
-- because the fold happened in the predicate; now that the folded value is the
-- column, the index is an ordinary one and any predicate spelled "= $1" uses it.
CREATE INDEX invoices_buyer_email_folded_idx ON invoices (buyer_email_folded);

-- 000002's expression index served ONE query, the erasure count, and that query
-- no longer spells its predicate that way. Left in place it would cost a write
-- on every invoice ever issued and serve no read -- the same trade 000009 of the
-- order module refuses by name.
DROP INDEX IF EXISTS invoices_buyer_email_idx;
