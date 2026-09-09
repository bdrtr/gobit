-- A rate can STAND ON another, and the tax of the one above is computed on the
-- tax of the one below.
--
-- # What forced it
--
-- Some markets levy two taxes on one line and the second is computed on the
-- first: a federal tax and then a provincial one on the amount including it.
-- gobit chose exactly ONE rate per line, so those markets could not be
-- expressed at all — the merchant could only pick the sum of two rates, which
-- is a different number whenever the second compounds.
--
-- # Why a LIST and not a set
--
-- The rates of a stack are applied IN ORDER and the order changes the answer.
-- A set with a position column would let two rates claim one position; a linked
-- list cannot — every rate stands on at most one, and at most one stands on it
-- (tax_rate_stacks_on_uniq). The order is then the list itself.
--
-- # Why rate SELECTION does not change
--
-- Exactly one rate is still chosen for a line, by the rules that were already
-- there. The chosen rate is then EXPANDED into the stack it heads. A rate that
-- stands on another is therefore never a candidate: it may not be a default and
-- it may not carry rules, both refused by the service at write time.
ALTER TABLE tax_rate
    ADD CONSTRAINT tax_rate_id_region_uniq UNIQUE (id, tax_region_id);

ALTER TABLE tax_rate
    ADD COLUMN IF NOT EXISTS stacks_on_id TEXT,
    -- compound says HOW this rate is computed: on the line's own amount
    -- (FALSE) or on that amount plus the taxes below it (TRUE). It is a
    -- separate word from the stack itself, because a market can levy a second
    -- tax on the SAME base — two taxes side by side rather than one on top of
    -- the other.
    ADD COLUMN IF NOT EXISTS compound BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE tax_rate
    ADD CONSTRAINT tax_rate_stacks_on_self_check
        CHECK (stacks_on_id IS NULL OR stacks_on_id <> id);

-- NULL-SAFE BY CONSTRUCTION: compound is NOT NULL, so neither side of the OR
-- can be NULL and the constraint cannot be satisfied by ignorance.
--   (NULL, TRUE)  -> FALSE OR FALSE -> REFUSED: nothing to compound on
--   (NULL, FALSE) -> FALSE OR TRUE  -> accepted
ALTER TABLE tax_rate
    ADD CONSTRAINT tax_rate_compound_check
        CHECK (stacks_on_id IS NOT NULL OR compound = FALSE);

-- A stack lives inside ONE region: the composite key carries the region so the
-- reference cannot cross into another one. It is the same trick, for the same
-- reason, as tax_region_parent_fk up the file.
ALTER TABLE tax_rate
    ADD CONSTRAINT tax_rate_stacks_on_fk
        FOREIGN KEY (stacks_on_id, tax_region_id) REFERENCES tax_rate (id, tax_region_id);

-- At most one rate stands DIRECTLY on any rate, which is what makes the stack a
-- list rather than a tree: with two, the order of the two would be undecided
-- and the same line could be taxed two ways.
CREATE UNIQUE INDEX IF NOT EXISTS tax_rate_stacks_on_uniq
    ON tax_rate (stacks_on_id)
    WHERE stacks_on_id IS NOT NULL AND deleted_at IS NULL;
