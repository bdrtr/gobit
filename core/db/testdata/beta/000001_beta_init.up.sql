-- The beta module's table. Per plan Section 2.2 there is NO foreign key to alpha.
CREATE TABLE beta_items (
    id         TEXT        PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
