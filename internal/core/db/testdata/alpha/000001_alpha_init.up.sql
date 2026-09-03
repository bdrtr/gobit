-- The alpha module's first table. For testing only.
CREATE TABLE alpha_items (
    id         TEXT        PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
