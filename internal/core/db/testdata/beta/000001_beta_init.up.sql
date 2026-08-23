-- beta modülünün tablosu. Plan Bölüm 2.2 gereği alpha'ya foreign key YOKTUR.
CREATE TABLE beta_items (
    id         TEXT        PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
