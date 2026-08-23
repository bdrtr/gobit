-- product modülünün şeması (Faz 4 — Katalog).
--
-- Konvansiyonlar (plan Bölüm 8):
--   * Kimlikler önekli metindir (prod_, variant_, popt_, poptval_, ...) ve
--     uygulama tarafından üretilir; veritabanı sıra/UUID üretmez.
--   * Zaman UTC'dir: created_at / updated_at / deleted_at. Silme SOFT'tur;
--     tüm okuma sorguları deleted_at IS NULL filtresi uygular.
--   * Benzersizlik KISMİ indeksle kurulur (WHERE deleted_at IS NULL): silinmiş
--     bir kaydın handle'ı yeni kaydın önünü tıkamamalıdır.
--   * Modül İÇİ foreign key serbesttir ve kullanılır. Başka modülün tablosuna
--     REFERENCES VERİLMEZ (Prensip 2.2): varyantın fiyatı ve stoğu link
--     tablolarında yaşar (product_variant_price_set, product_variant_inventory).
--   * Para birimi ya da tutar bu modülde YOKTUR; fiyat pricing modülünündür.

CREATE TABLE IF NOT EXISTS product_collection (
    id         text PRIMARY KEY,
    title      text        NOT NULL,
    handle     text        NOT NULL,
    metadata   jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz
);

CREATE UNIQUE INDEX IF NOT EXISTS product_collection_handle_uniq
    ON product_collection (handle) WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS product_category (
    id          text PRIMARY KEY,
    name        text        NOT NULL,
    handle      text        NOT NULL,
    description text,
    -- Kendine referans: kategori ağacı aynı modülün içindedir.
    parent_id   text REFERENCES product_category (id) ON DELETE SET NULL,
    is_active   boolean     NOT NULL DEFAULT true,
    is_internal boolean     NOT NULL DEFAULT false,
    rank        integer     NOT NULL DEFAULT 0,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    deleted_at  timestamptz
);

CREATE UNIQUE INDEX IF NOT EXISTS product_category_handle_uniq
    ON product_category (handle) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS product_category_parent_idx
    ON product_category (parent_id) WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS product_tag (
    id         text PRIMARY KEY,
    value      text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz
);

CREATE UNIQUE INDEX IF NOT EXISTS product_tag_value_uniq
    ON product_tag (value) WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS product (
    id             text PRIMARY KEY,
    handle         text        NOT NULL,
    title          text        NOT NULL,
    subtitle       text,
    description    text,
    thumbnail      text,
    status         text        NOT NULL DEFAULT 'draft'
        CONSTRAINT product_status_check CHECK (status IN ('draft', 'published', 'archived')),
    is_giftcard    boolean     NOT NULL DEFAULT false,
    discountable   boolean     NOT NULL DEFAULT true,
    -- Ağırlık gram, ölçüler milimetredir: plan Bölüm 8'in tam sayı kuralı
    -- yalnızca para için değil, ölçüler için de kayan noktayı dışarıda tutar.
    weight         integer,
    length         integer,
    height         integer,
    width          integer,
    material       text,
    origin_country text,
    collection_id  text REFERENCES product_collection (id) ON DELETE SET NULL,
    metadata       jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    deleted_at     timestamptz
);

CREATE UNIQUE INDEX IF NOT EXISTS product_handle_uniq
    ON product (handle) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS product_status_idx
    ON product (status) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS product_collection_idx
    ON product (collection_id) WHERE deleted_at IS NULL;
-- Listeleme sırası (created_at DESC, id DESC) ile aynı indeks: sayfalama
-- kalıcı sıra ister, id ikinci anahtar olarak eşitliği bozar.
CREATE INDEX IF NOT EXISTS product_created_at_idx
    ON product (created_at DESC, id DESC) WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS product_variant (
    id               text PRIMARY KEY,
    product_id       text        NOT NULL REFERENCES product (id) ON DELETE CASCADE,
    title            text        NOT NULL,
    sku              text,
    barcode          text,
    ean              text,
    upc              text,
    manage_inventory boolean     NOT NULL DEFAULT true,
    allow_backorder  boolean     NOT NULL DEFAULT false,
    weight           integer,
    rank             integer     NOT NULL DEFAULT 0,
    metadata         jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    deleted_at       timestamptz
);

CREATE INDEX IF NOT EXISTS product_variant_product_idx
    ON product_variant (product_id, rank, id) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS product_variant_sku_uniq
    ON product_variant (sku) WHERE deleted_at IS NULL AND sku IS NOT NULL;

CREATE TABLE IF NOT EXISTS product_option (
    id         text PRIMARY KEY,
    product_id text        NOT NULL REFERENCES product (id) ON DELETE CASCADE,
    title      text        NOT NULL,
    rank       integer     NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz
);

CREATE INDEX IF NOT EXISTS product_option_product_idx
    ON product_option (product_id, rank, id) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS product_option_title_uniq
    ON product_option (product_id, title) WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS product_option_value (
    id         text PRIMARY KEY,
    option_id  text        NOT NULL REFERENCES product_option (id) ON DELETE CASCADE,
    value      text        NOT NULL,
    rank       integer     NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz
);

CREATE INDEX IF NOT EXISTS product_option_value_option_idx
    ON product_option_value (option_id, rank, id) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS product_option_value_uniq
    ON product_option_value (option_id, value) WHERE deleted_at IS NULL;

-- Varyantın seçenek değerleriyle ilişkisi.
--
-- Birincil anahtarın (variant_id, option_id) olması kuralı şemaya yazar: bir
-- varyant aynı seçenekten YALNIZCA BİR değer taşır ("Beden: S" ve "Beden: M"
-- aynı varyantta olamaz). Bu, uygulama katmanında "önce oku sonra yaz" ile
-- korunsaydı eşzamanlı iki istek arasından kaçardı.
CREATE TABLE IF NOT EXISTS product_variant_option_value (
    variant_id text        NOT NULL REFERENCES product_variant (id) ON DELETE CASCADE,
    option_id  text        NOT NULL REFERENCES product_option (id) ON DELETE CASCADE,
    value_id   text        NOT NULL REFERENCES product_option_value (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (variant_id, option_id)
);

CREATE INDEX IF NOT EXISTS product_variant_option_value_value_idx
    ON product_variant_option_value (value_id);

CREATE TABLE IF NOT EXISTS product_image (
    id         text PRIMARY KEY,
    product_id text        NOT NULL REFERENCES product (id) ON DELETE CASCADE,
    url        text        NOT NULL,
    rank       integer     NOT NULL DEFAULT 0,
    metadata   jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz
);

CREATE INDEX IF NOT EXISTS product_image_product_idx
    ON product_image (product_id, rank, id) WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS product_tag_map (
    product_id text        NOT NULL REFERENCES product (id) ON DELETE CASCADE,
    tag_id     text        NOT NULL REFERENCES product_tag (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (product_id, tag_id)
);

CREATE INDEX IF NOT EXISTS product_tag_map_tag_idx ON product_tag_map (tag_id);

CREATE TABLE IF NOT EXISTS product_category_map (
    product_id  text        NOT NULL REFERENCES product (id) ON DELETE CASCADE,
    category_id text        NOT NULL REFERENCES product_category (id) ON DELETE CASCADE,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (product_id, category_id)
);

CREATE INDEX IF NOT EXISTS product_category_map_category_idx
    ON product_category_map (category_id);
