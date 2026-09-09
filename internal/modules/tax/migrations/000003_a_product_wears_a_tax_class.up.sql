-- A product wears a tax class, and a rate can be written for the class.
--
-- # What forced it
--
-- A rule could name ONE product ('product') or a type the catalog does not have
-- ('product_type', empty on every request gobit sends today). So a shop taxing
-- books at one rate and electronics at another had to write one rule per
-- product and rewrite them as the catalog grew — the classification existed in
-- the merchant's head and nowhere in the schema.
--
-- # Why the class lives in THIS module
--
-- The rate is chosen here, and a classification that only tax reads is tax's
-- own. Putting it on the product would put a tax word in the catalog's model
-- and in the storefront body it is embedded in, and would make the cart's tax
-- leg read the catalog a second time to answer a question this module can
-- answer from its own tables.
--
-- The membership is therefore a table here, keyed by a free-TEXT product id for
-- the reason tax_rate_rule.reference_id gives: a cross-module foreign key is
-- banned (Principle 2.2), so a deleted product can leave a membership behind —
-- harmless, because no line with that id enters a calculation any more.
CREATE TABLE IF NOT EXISTS tax_class (
    id         TEXT        PRIMARY KEY,
    name       TEXT        NOT NULL,
    metadata   JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ,

    CONSTRAINT tax_class_name_check CHECK (name <> '')
);

-- The name is what an operator picks the class by, so two live classes may not
-- share one. A retired class keeps its name out of the way: the index is
-- partial, exactly like tax_rate_default_uniq one table over.
CREATE UNIQUE INDEX IF NOT EXISTS tax_class_name_uniq
    ON tax_class (name)
    WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS tax_class_member (
    id           TEXT        PRIMARY KEY,
    tax_class_id TEXT        NOT NULL,
    product_id   TEXT        NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at   TIMESTAMPTZ,

    CONSTRAINT tax_class_member_product_check CHECK (product_id <> ''),
    CONSTRAINT tax_class_member_class_fk
        FOREIGN KEY (tax_class_id) REFERENCES tax_class (id)
);

-- A product is in AT MOST ONE class, and this index is why the selection stays
-- decidable: with two classes a line would carry two class keys of equal
-- specificity and which rate applied would fall to row order — the same failure
-- tax_rate_default_uniq exists to prevent one table over.
CREATE UNIQUE INDEX IF NOT EXISTS tax_class_member_product_uniq
    ON tax_class_member (product_id)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS tax_class_member_class_idx
    ON tax_class_member (tax_class_id)
    WHERE deleted_at IS NULL;

-- The fourth reference a rule may name. The vocabulary is closed here and in
-- models.RuleReference, and the two are compared by a test: a word the schema
-- accepts and the code does not is a rule nobody can write, and the reverse is
-- a rule the database refuses at the last moment.
ALTER TABLE tax_rate_rule DROP CONSTRAINT tax_rate_rule_reference_check;

ALTER TABLE tax_rate_rule
    ADD CONSTRAINT tax_rate_rule_reference_check
        CHECK (reference IN ('product', 'tax_class', 'product_type', 'shipping_option'));
