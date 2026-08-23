-- alpha modülünün ilk tablosu. Yalnızca test amaçlıdır.
CREATE TABLE alpha_items (
    id         TEXT        PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
