-- order_line_taxes is the per-rate breakdown of a line taxed by a STACK.
--
-- # Why the line's own rate was not enough
--
-- 000004 put tax_rate_bps on the line and argued that an invoice has to print
-- the rate the customer was CHARGED under rather than one recomputed from a
-- rounded amount. That argument holds and this table is its second half: since
-- ADR 0095 a line can be taxed by several rates at once, and the line can hold
-- only one of them. It holds the stack's BASE -- really applied, on a really
-- recorded amount -- so nothing on the line is wrong; what is missing is the
-- rest. A line taxed at 5% + 8% prints "5%" today, and the customer's own
-- arithmetic disagrees with it.
--
-- # Why rows and not a JSONB column on the line
--
-- The constraints. A rate outside [0, 10000], a component taking more than its
-- own base, two components claiming the same position: each of those is a
-- CHECK here and none of them is expressible over a JSON document. The module's
-- standing answer is that the application layer is not the last defence, and a
-- blob would have made it the only one.
--
-- The sum identity -- the components add up to the line's tax_total -- is the
-- one rule that stays in the service, because it spans rows and a CHECK cannot
-- see across them. It is checked at every boundary the breakdown crosses
-- (tax, cart, order) rather than at one.
--
-- # Why position exists here and not in the tax module
--
-- In the tax module a stack is a chain: each rate names the one below it and a
-- partial unique index keeps that chain from branching, so the order is derived
-- and a position column would be a second, disagreeable source of truth
-- (ADR 0095). What arrives here is an ARRAY, and the chain is gone with it. A
-- compound component's base is everything below it, so the order is what makes
-- the figures reproducible, and it has to be stored.
--
-- # Why there is no row for a single-rate line
--
-- A breakdown of one says nothing the line does not, and its absence is what
-- lets a reader take "this line has components" to mean "a stack taxed it".
-- Almost every line in a shop is taxed by one rate; writing a row for each
-- would double the module's largest table to record nothing.
CREATE TABLE IF NOT EXISTS order_line_taxes (
    id                 TEXT        PRIMARY KEY,
    order_line_item_id TEXT        NOT NULL
        REFERENCES order_line_items (id) ON DELETE CASCADE,
    -- position is the component's place in the stack, base first, from 0.
    position           INTEGER     NOT NULL,
    -- rate_id is the tax module's id; there is NO FK (Principle 2.2). It is
    -- empty when an external provider carries no ids of its own.
    rate_id            TEXT        NOT NULL DEFAULT '',
    rate_bps           INTEGER     NOT NULL,
    -- compound says the component was computed on the line's amount PLUS the
    -- taxes below it.
    compound           BOOLEAN     NOT NULL DEFAULT FALSE,
    taxable_amount     BIGINT      NOT NULL,
    tax_amount         BIGINT      NOT NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT order_line_taxes_position_nonneg  CHECK (position >= 0),
    CONSTRAINT order_line_taxes_rate_bps_range   CHECK (rate_bps >= 0 AND rate_bps <= 10000),
    CONSTRAINT order_line_taxes_taxable_nonneg   CHECK (taxable_amount >= 0),
    -- The same bound the line carries, one component at a time: a rate is at
    -- most 100%, so a component can never take more than the base it was
    -- computed on. A line whose total stays inside its own amount can still
    -- hide a component that does not.
    CONSTRAINT order_line_taxes_within_base
        CHECK (tax_amount >= 0 AND tax_amount <= taxable_amount),
    -- The first component stands on nothing, so it cannot compound.
    CONSTRAINT order_line_taxes_compound_needs_base
        CHECK (position > 0 OR compound = FALSE)
);

-- The position is unique per line: two components in the same place would make
-- the order ambiguous, and a compound component's base is defined by the order.
CREATE UNIQUE INDEX IF NOT EXISTS order_line_taxes_position_uniq
    ON order_line_taxes (order_line_item_id, position);
