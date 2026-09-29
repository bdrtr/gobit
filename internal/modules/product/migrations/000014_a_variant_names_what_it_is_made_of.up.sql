-- product_bundle_component: the variants a bundle variant is made of, and how
-- many of each (ADR 0234).
--
-- A starter kit is one sellable variant, a kettle and two mugs inside. The
-- rows are the bundle's composition in the operator's order; a bundle is not a
-- component of another, which the service holds, and a component that is part
-- of a live bundle is not deleted, which the service refuses.
CREATE TABLE IF NOT EXISTS product_bundle_component (
    bundle_variant_id    text        NOT NULL REFERENCES product_variant (id) ON DELETE CASCADE,
    component_variant_id text        NOT NULL REFERENCES product_variant (id) ON DELETE CASCADE,
    quantity             integer     NOT NULL,
    rank                 integer     NOT NULL,
    created_at           timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (bundle_variant_id, component_variant_id),
    CONSTRAINT product_bundle_component_not_itself CHECK (bundle_variant_id <> component_variant_id),
    CONSTRAINT product_bundle_component_quantity CHECK (quantity BETWEEN 1 AND 100),
    CONSTRAINT product_bundle_component_rank_nonneg CHECK (rank >= 0),
    CONSTRAINT product_bundle_component_rank_unique UNIQUE (bundle_variant_id, rank)
);

-- A component's deletion asks which bundles hold it; this finds them.
CREATE INDEX IF NOT EXISTS product_bundle_component_component_idx
    ON product_bundle_component (component_variant_id);
