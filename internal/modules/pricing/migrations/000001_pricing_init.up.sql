-- pricing modülünün şeması (plan Faz 4, Bölüm 6).
--
-- Tablolar YALNIZCA bu modüle aittir. Prensip 2.2 gereği başka bir modülün
-- tablosuna REFERENCES verilmez: bir varyantın fiyat kabına bağlanması
-- Module Links üzerinden yapılır ve pricing o bağı hiç görmez. Modülün KENDİ
-- tabloları arasındaki foreign key'ler ise serbesttir ve kullanılır.
--
-- Zaman sütunları TIMESTAMPTZ'dir ve daima UTC yazılır; silme SOFT'tur
-- (deleted_at) ve tüm okuma sorguları deleted_at IS NULL filtresi uygular.

-- price_list kampanya/segment fiyat listesidir.
-- Bir listeye bağlı fiyat, listenin durumu ve tarih penceresi uygunken geçerlidir.
CREATE TABLE IF NOT EXISTS price_list (
    id          TEXT PRIMARY KEY,
    title       TEXT        NOT NULL,
    description TEXT        NOT NULL DEFAULT '',
    type        TEXT        NOT NULL,
    status      TEXT        NOT NULL,
    starts_at   TIMESTAMPTZ,
    ends_at     TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at  TIMESTAMPTZ,
    CONSTRAINT price_list_title_check  CHECK (title <> ''),
    CONSTRAINT price_list_type_check   CHECK (type IN ('sale', 'override')),
    CONSTRAINT price_list_status_check CHECK (status IN ('draft', 'active', 'expired')),
    CONSTRAINT price_list_window_check CHECK (starts_at IS NULL OR ends_at IS NULL OR starts_at < ends_at)
);

-- price_set bir varyantın fiyatlarının kabıdır.
-- Kabın kendisi hangi varyanta ait olduğunu BİLMEZ; bağ link tablosundadır.
CREATE TABLE IF NOT EXISTS price_set (
    id         TEXT PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ
);

-- price tek bir para birimi/adet aralığı için tutardır.
--
-- amount TAM SAYI minor unit'tir (kuruş/cent); float kullanılmaz ve para birimi
-- ayrı sütunda durur (plan Bölüm 8). Üst sınır bilinçlidir: amount * max_quantity
-- çarpımı int64'e sığmalıdır, aksi hâlde sepet toplamı sessizce taşardı.
CREATE TABLE IF NOT EXISTS price (
    id            TEXT PRIMARY KEY,
    price_set_id  TEXT        NOT NULL REFERENCES price_set(id) ON DELETE CASCADE,
    price_list_id TEXT        REFERENCES price_list(id) ON DELETE CASCADE,
    currency_code TEXT        NOT NULL,
    amount        BIGINT      NOT NULL,
    min_quantity  INTEGER     NOT NULL DEFAULT 1,
    max_quantity  INTEGER,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at    TIMESTAMPTZ,
    CONSTRAINT price_currency_check       CHECK (currency_code ~ '^[A-Z]{3}$'),
    CONSTRAINT price_amount_check         CHECK (amount >= 0 AND amount <= 1000000000000),
    CONSTRAINT price_min_quantity_check   CHECK (min_quantity >= 1 AND min_quantity <= 1000000),
    CONSTRAINT price_max_quantity_check   CHECK (max_quantity IS NULL OR max_quantity <= 1000000),
    CONSTRAINT price_quantity_range_check CHECK (max_quantity IS NULL OR max_quantity >= min_quantity)
);

CREATE INDEX IF NOT EXISTS price_set_id_idx ON price (price_set_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS price_list_id_idx ON price (price_list_id) WHERE deleted_at IS NULL;

-- price_rule bir fiyatın hangi koşulda geçerli olduğunu belirler.
--
-- Koşul (attribute, operator, rule_values) üçlüsüdür; örn.
-- ("region_id", "eq", {"reg_1"}) ya da ("customer_group_id", "in", {"vip","b2b"}).
-- Sütun adı "values" DEĞİLDİR: VALUES PostgreSQL'de ayrılmış bir sözcüktür ve
-- tırnaklanmadan kullanılamazdı.
CREATE TABLE IF NOT EXISTS price_rule (
    id          TEXT PRIMARY KEY,
    price_id    TEXT        NOT NULL REFERENCES price(id) ON DELETE CASCADE,
    attribute   TEXT        NOT NULL,
    operator    TEXT        NOT NULL,
    rule_values TEXT[]      NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at  TIMESTAMPTZ,
    CONSTRAINT price_rule_attribute_check CHECK (attribute <> ''),
    CONSTRAINT price_rule_operator_check  CHECK (operator IN ('eq', 'ne', 'in', 'nin', 'gt', 'gte', 'lt', 'lte')),
    CONSTRAINT price_rule_values_check    CHECK (array_length(rule_values, 1) >= 1)
);

CREATE INDEX IF NOT EXISTS price_rule_price_id_idx ON price_rule (price_id) WHERE deleted_at IS NULL;
