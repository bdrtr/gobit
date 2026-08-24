-- fulfillment modülünün şeması (plan Faz 7).
--
-- Sahiplik: buradaki altı tablo YALNIZCA fulfillment modülüne aittir. Modül
-- içi foreign key'ler serbesttir ve kullanılır; başka bir modülün tablosuna
-- REFERENCES VERİLMEZ (Prensip 2.2 — cross-module FK yasağı). Bu yüzden
-- shipping_options.region_id (region modülünün kimliği), fulfillments.reference
-- (sipariş kimliği) ve fulfillment_items.line_item_id (sipariş satırı kimliği)
-- FK DEĞİLDİR: ilişki Module Links üzerinden kurulur ve link tablosu
-- çekirdektedir.
--
-- Para: tüm tutarlar BIGINT ve minor unit'tir (kuruş/cent); para birimi AYRI
-- sütunda durur (plan Bölüm 8). NUMERIC ya da kayan nokta hiçbir yerde
-- kullanılmaz — kargo ücretinin kuruşu, sipariş toplamına birebir girer.
--
-- Zaman: tüm damgalar timestamptz (UTC). Silme yumuşaktır (deleted_at) ve tüm
-- okuma sorguları deleted_at IS NULL filtresi uygular. Tek istisna manuel
-- sağlayıcının defteridir; gerekçesi kendi tablosunun başındadır.

-- shipping_profiles kargo profilidir: hangi ürünlerin hangi kargo kurallarına
-- tabi olduğunu gruplayan kaptır.
--
-- Ürünler profillere Module Links ile bağlanır; profil hangi ürünlere bağlı
-- olduğunu BİLMEZ (Prensip 2.1). "default" profil her mağazada bir tanedir ve
-- ürünlerin varsayılan bağlandığı yerdir; "gift_card" fiziksel gönderi
-- gerektirmeyen ürünler için ayrılmıştır.
CREATE TABLE IF NOT EXISTS shipping_profiles (
    id         TEXT        PRIMARY KEY,
    name       TEXT        NOT NULL,
    type       TEXT        NOT NULL DEFAULT 'default',
    metadata   JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ,

    CONSTRAINT shipping_profiles_name_check CHECK (name <> ''),
    CONSTRAINT shipping_profiles_type_valid CHECK (type IN ('default', 'gift_card', 'custom'))
);

-- Profil adı YAŞAYAN kayıtlar arasında tektir: aynı adı taşıyan iki profil,
-- yöneticinin hangi kuralı düzenlediğini bilememesi demektir.
CREATE UNIQUE INDEX IF NOT EXISTS shipping_profiles_name_uniq
    ON shipping_profiles (name)
    WHERE deleted_at IS NULL;

-- shipping_options bir kargo seçeneğidir: müşteriye sunulan "Standart kargo",
-- "Hızlı kargo", "Mağazadan teslim" gibi satırların kaynağıdır.
--
-- price_type iki değer alır:
--   flat       — ücret bu satırdaki amount'tur; sağlayıcıya HİÇ gidilmez.
--   calculated — ücreti sağlayıcının Quote'u belirler; amount kullanılmaz ve
--                sıfır olmak ZORUNDADIR (aşağıdaki kısıt). İki kaynaklı bir
--                fiyat, hangisinin geçerli olduğunu okuyana bırakırdı.
--
-- data sağlayıcıya ait YAPILANDIRMADIR (örn. kilogram başına ücret) ve Quote
-- çağrısına olduğu gibi geçirilir; metadata ise mağazanın kendi serbest
-- verisidir. İkisi ayrıdır çünkü data mağaza yüzeyine ÇIKMAZ.
CREATE TABLE IF NOT EXISTS shipping_options (
    id                  TEXT        PRIMARY KEY,
    name                TEXT        NOT NULL,
    provider_id         TEXT        NOT NULL,
    shipping_profile_id TEXT        NOT NULL REFERENCES shipping_profiles (id) ON DELETE CASCADE,
    price_type          TEXT        NOT NULL DEFAULT 'flat',
    amount              BIGINT      NOT NULL DEFAULT 0,
    currency_code       TEXT        NOT NULL,
    -- region_id region modülünün kimliğidir; FK YOKTUR (Prensip 2.2).
    -- Boş dize "her bölge" demektir.
    region_id           TEXT        NOT NULL DEFAULT '',
    is_return           BOOLEAN     NOT NULL DEFAULT FALSE,
    admin_only          BOOLEAN     NOT NULL DEFAULT FALSE,
    data                JSONB       NOT NULL DEFAULT '{}'::jsonb,
    metadata            JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at          TIMESTAMPTZ,

    CONSTRAINT shipping_options_name_check      CHECK (name <> ''),
    CONSTRAINT shipping_options_provider_check  CHECK (provider_id <> ''),
    CONSTRAINT shipping_options_price_type_valid CHECK (price_type IN ('flat', 'calculated')),
    -- Sıfır tutar geçerlidir ve "ücretsiz kargo" demektir; negatif tutar,
    -- müşteriye kargodan para ödemek demek olurdu.
    CONSTRAINT shipping_options_amount_nonneg   CHECK (amount >= 0),
    CONSTRAINT shipping_options_amount_max      CHECK (amount <= 1000000000000),
    -- Hesaplanan seçenekte tutar sağlayıcıdan gelir; satırdaki değer BAYAT bir
    -- ikinci kaynak olurdu. Servis bunu zaten reddeder; buradaki kısıt son
    -- savunmadır ve doğrudan SQL ile yapılan bir müdahaleyi de durdurur.
    CONSTRAINT shipping_options_calculated_zero CHECK (price_type <> 'calculated' OR amount = 0),
    CONSTRAINT shipping_options_currency_format CHECK (currency_code ~ '^[A-Z]{3}$')
);

CREATE INDEX IF NOT EXISTS shipping_options_region_idx
    ON shipping_options (region_id, currency_code)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS shipping_options_profile_idx
    ON shipping_options (shipping_profile_id)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS shipping_options_alive_idx
    ON shipping_options (created_at DESC, id DESC)
    WHERE deleted_at IS NULL;

-- shipping_option_rules bir seçeneğin HANGİ KOŞULDA sunulacağını belirler.
--
-- Koşul (attribute, operator, rule_values) üçlüsüdür; örn.
-- ("subtotal", "gte", {"50000"}) — "ara toplam 50.000 kuruşu geçerse ücretsiz
-- kargo". Sütun adı "values" DEĞİLDİR: VALUES PostgreSQL'de ayrılmış bir
-- sözcüktür ve tırnaklanmadan kullanılamazdı (pricing modülündeki price_rule
-- ile aynı gerekçe).
--
-- Bir seçeneğin TÜM kuralları eşleşmelidir; kuralsız seçenek koşulsuzdur.
CREATE TABLE IF NOT EXISTS shipping_option_rules (
    id                 TEXT        PRIMARY KEY,
    shipping_option_id TEXT        NOT NULL REFERENCES shipping_options (id) ON DELETE CASCADE,
    attribute          TEXT        NOT NULL,
    operator           TEXT        NOT NULL,
    rule_values        TEXT[]      NOT NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at         TIMESTAMPTZ,

    CONSTRAINT shipping_option_rules_attribute_check CHECK (attribute <> ''),
    CONSTRAINT shipping_option_rules_operator_check
        CHECK (operator IN ('eq', 'ne', 'in', 'nin', 'gt', 'gte', 'lt', 'lte')),
    -- cardinality boş dizi için 0 döner, NULL değil; array_length ile yazılsaydı
    -- sonuç NULL olur ve NULL dönen bir CHECK SAĞLANMIŞ sayılırdı — kısıt
    -- fiilen hiçbir şeyi engellemezdi (bkz. pricing 000002).
    CONSTRAINT shipping_option_rules_values_check CHECK (cardinality(rule_values) >= 1)
);

CREATE INDEX IF NOT EXISTS shipping_option_rules_option_idx
    ON shipping_option_rules (shipping_option_id)
    WHERE deleted_at IS NULL;

-- fulfillments gerçekleşmiş bir gönderidir.
--
-- reference çağıranın kendi kaydının kimliğidir (sipariş). FK YOKTUR
-- (Prensip 2.2); bağ Module Links ile kurulur.
--
-- external_id sağlayıcının kendi gönderi kimliğidir; mutabakatta iki sistemi
-- eşleştiren alan budur. Gönderi satırı sağlayıcıya GİTMEDEN ÖNCE yazıldığı
-- için başlangıçta boştur ve sağlayıcı yanıtından sonra doldurulur.
CREATE TABLE IF NOT EXISTS fulfillments (
    id                 TEXT        PRIMARY KEY,
    reference          TEXT        NOT NULL,
    shipping_option_id TEXT        NOT NULL REFERENCES shipping_options (id) ON DELETE RESTRICT,
    provider_id        TEXT        NOT NULL,
    external_id        TEXT        NOT NULL DEFAULT '',
    status             TEXT        NOT NULL DEFAULT 'pending',
    tracking_number    TEXT        NOT NULL DEFAULT '',
    tracking_url       TEXT        NOT NULL DEFAULT '',
    -- idempotency_key aynı gönderinin iki kez oluşturulmasını engeller
    -- (plan Bölüm 2.6). Anahtarsız bir tekrar, İKİNCİ BİR KARGO ETİKETİ demek
    -- olurdu.
    idempotency_key    TEXT        NOT NULL,
    shipped_at         TIMESTAMPTZ,
    delivered_at       TIMESTAMPTZ,
    canceled_at        TIMESTAMPTZ,
    data               JSONB       NOT NULL DEFAULT '{}'::jsonb,
    metadata           JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at         TIMESTAMPTZ,

    CONSTRAINT fulfillments_reference_check CHECK (reference <> ''),
    CONSTRAINT fulfillments_provider_check  CHECK (provider_id <> ''),
    CONSTRAINT fulfillments_key_check       CHECK (idempotency_key <> ''),
    CONSTRAINT fulfillments_status_valid
        CHECK (status IN ('pending', 'shipped', 'delivered', 'canceled')),
    -- Durum ile zaman damgaları birbirini tutmalıdır: "shipped" bir gönderinin
    -- sevk anı, "delivered" olanın teslim anı, "canceled" olanın iptal anı
    -- YAZILMIŞ olmalıdır. Damgasız bir durum, mutabakatta "ne zaman?" sorusunu
    -- cevapsız bırakırdı.
    CONSTRAINT fulfillments_shipped_stamp   CHECK (status <> 'shipped'   OR shipped_at   IS NOT NULL),
    CONSTRAINT fulfillments_delivered_stamp CHECK (status <> 'delivered' OR delivered_at IS NOT NULL),
    CONSTRAINT fulfillments_canceled_stamp  CHECK (status <> 'canceled'  OR canceled_at  IS NOT NULL)
);

-- Bir idempotency anahtarı YAŞAYAN gönderiler arasında tektir. Saga bir adımı
-- yeniden denediğinde ikinci Create bu indekse ON CONFLICT DO NOTHING ile
-- çarpar, satır yazmaz ve mevcut gönderiyi okur; indeks yarışın tek noktasıdır.
CREATE UNIQUE INDEX IF NOT EXISTS fulfillments_idempotency_uniq
    ON fulfillments (idempotency_key)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS fulfillments_reference_idx
    ON fulfillments (reference, created_at DESC)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS fulfillments_alive_idx
    ON fulfillments (created_at DESC, id DESC)
    WHERE deleted_at IS NULL;

-- fulfillment_items gönderiye giren kalemlerdir.
--
-- line_item_id sipariş satırının kimliğidir; FK YOKTUR (Prensip 2.2) ve bu
-- modülde doğrulanmaz. Adet BIGINT'tir ve pozitiftir.
CREATE TABLE IF NOT EXISTS fulfillment_items (
    id             TEXT        PRIMARY KEY,
    fulfillment_id TEXT        NOT NULL REFERENCES fulfillments (id) ON DELETE CASCADE,
    line_item_id   TEXT        NOT NULL,
    quantity       BIGINT      NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT fulfillment_items_line_check     CHECK (line_item_id <> ''),
    -- Adedin ÜST sınırı da şemadadır. Servis aynı sınırı zaten uygular
    -- (models.MaxQuantity), ama uygulama katmanı tek başına son savunma
    -- değildir: adet ücretle ÇARPILAN bir sayıdır ve sınırsız bir değer,
    -- çarpımı int64'ten taşırıp negatif bir tutara çevirebilir. Doğrudan SQL
    -- çalıştıran bir bakım betiği de bu kısıda takılır.
    CONSTRAINT fulfillment_items_quantity_check CHECK (quantity > 0),
    CONSTRAINT fulfillment_items_quantity_max   CHECK (quantity <= 1000000)
);

-- Aynı sipariş satırı bir gönderide İKİ KEZ yer alamaz; iki satır, adedin
-- hangisinin geçerli olduğunu okuyana bırakırdı.
CREATE UNIQUE INDEX IF NOT EXISTS fulfillment_items_line_uniq
    ON fulfillment_items (fulfillment_id, line_item_id);

-- fulfillment_manual_shipments MANUEL sağlayıcının kendi defteridir.
--
-- Neden AYRI bir tablo: manual sağlayıcı gerçek bir kargo firmasını TAKLİT
-- eder. Gerçek sağlayıcının durumu kendi sistemindedir ve modül ona yalnızca
-- FulfillmentProvider arayüzünden ulaşır. Aynı ayrımın burada da korunması,
-- modülün kazara sağlayıcının iç durumunu okumasını yapısal olarak engeller:
-- fulfillment servisi bu tabloya HİÇ dokunmaz.
--
-- Neden BELLEKTE DEĞİL: payment modülündeki manuel sağlayıcıyla aynı gerekçe.
-- Süreç yeniden başladığında oluşturulmuş bir gönderi bulunabilmelidir; saga
-- telafisi (Cancel) tam da sürecin düştüğü senaryoda çalışmak zorundadır ve
-- birden çok süreç aynı gönderiyi görmelidir.
--
-- Yumuşak silme YOKTUR: bu tablo modülün alan verisi değil, taklit edilen dış
-- sistemin defteridir; kayıtları hiç silinmez.
CREATE TABLE IF NOT EXISTS fulfillment_manual_shipments (
    id              TEXT        PRIMARY KEY,
    idempotency_key TEXT        NOT NULL,
    reference       TEXT        NOT NULL,
    option_id       TEXT        NOT NULL,
    status          TEXT        NOT NULL DEFAULT 'pending',
    tracking_number TEXT        NOT NULL DEFAULT '',
    tracking_url    TEXT        NOT NULL DEFAULT '',
    data            JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT fulfillment_manual_shipments_reference_check CHECK (reference <> ''),
    CONSTRAINT fulfillment_manual_shipments_key_check       CHECK (idempotency_key <> ''),
    CONSTRAINT fulfillment_manual_shipments_status_valid
        CHECK (status IN ('pending', 'shipped', 'delivered', 'canceled'))
);

-- Aynı idempotency anahtarı İKİNCİ bir gönderi açamaz. Sağlayıcı sözleşmesinin
-- (internal/core/provider) idempotency şartını nihai olarak zorlayan kısıt budur.
CREATE UNIQUE INDEX IF NOT EXISTS fulfillment_manual_shipments_idempotency_uniq
    ON fulfillment_manual_shipments (idempotency_key);
