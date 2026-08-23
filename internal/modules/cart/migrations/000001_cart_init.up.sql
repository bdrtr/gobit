-- cart modülünün şeması (plan Faz 5).
--
-- Sahiplik: buradaki dört tablo YALNIZCA cart modülüne aittir. Modül İÇİ
-- foreign key'ler serbesttir ve kullanılır (satırlar, adresler ve kargo
-- yöntemleri sepete ON DELETE CASCADE ile bağlıdır); başka bir modülün
-- tablosuna REFERENCES VERİLMEZ (Prensip 2.2 — cross-module FK yasağı).
-- Bu yüzden carts.region_id, carts.customer_id ve cart_line_items.variant_id
-- serbest METİNDİR: ilişki Module Links üzerinden kurulur.
--
-- Para: TÜM tutarlar BIGINT ve minor unit'tir (kuruş/cent); para birimi ayrı
-- sütunda durur (plan Bölüm 8). Kayan nokta hiçbir yerde kullanılmaz.
--
-- Zaman: tüm damgalar timestamptz (UTC). Silme yumuşaktır (deleted_at) ve tüm
-- okuma sorguları deleted_at IS NULL süzer.

-- carts bir alışveriş sepetidir.
--
-- customer_id NULL olabilir: misafir sepeti kimliği olmayan bir müşteriye
-- aittir ve e-posta ile takip edilir.
--
-- TOPLAM ALANLARI (subtotal, discount_total, tax_total, shipping_total, total)
-- bu modül tarafından HESAPLANMAZ. Hesap, fiyatı pricing'den ve vergiyi
-- tax/region'dan alan calculate_totals WORKFLOW'una aittir (plan Bölüm 2.5,
-- ADR 0006); modül yalnızca SAKLAR ve DOĞRULAR. Doğrulamanın veritabanı
-- düzeyindeki karşılığı carts_totals_consistent kısıtıdır: kimlik bozulduğunda
-- satır hiç yazılmaz. Servis aynı kontrolü daha okunabilir bir hatayla önce
-- yapar; buradaki kısıt son savunmadır ve doğrudan SQL ile yapılan bir
-- müdahaleyi de kapsar.
--
-- revision / totals_revision BAYAT TOPLAMI GÖRÜNÜR KILAR. Sepetin şeklini
-- değiştiren her işlem (satır ekleme/güncelleme/silme, adres yazma, kargo
-- yöntemi ekleme/kaldırma) revision'ı bir artırır; toplamları yazan
-- calculate_totals ise totals_revision'a o anki revision'ı damgalar. İkisi
-- eşitse toplamlar sepetin GÜNCEL şekline aittir. Alternatifler kötüydü:
-- bayat toplamı sessizce saklamak müşteriye yanlış tutar göstermek, toplamları
-- sıfırlamak ise "bedava" demekti. Burada bayatlık ne gizlenir ne uydurulur —
-- okunabilir bir olgudur ve Faz 6'daki complete_cart bayat sepeti reddedebilir.
CREATE TABLE IF NOT EXISTS carts (
    id              TEXT        PRIMARY KEY,
    -- region_id region modülünün kimliğidir; FK YOKTUR (Prensip 2.2).
    region_id       TEXT        NOT NULL,
    -- customer_id customer modülünün kimliğidir; NULL ise sepet misafirindir.
    customer_id     TEXT,
    email           TEXT,
    -- currency_code ISO 4217 kodudur ve BÜYÜK harf saklanır. Değeri region'dan
    -- KOPYALAYAN taraf workflow'dur; cart modülü region'ı çağırmaz.
    currency_code   TEXT        NOT NULL,
    subtotal        BIGINT      NOT NULL DEFAULT 0,
    discount_total  BIGINT      NOT NULL DEFAULT 0,
    tax_total       BIGINT      NOT NULL DEFAULT 0,
    shipping_total  BIGINT      NOT NULL DEFAULT 0,
    total           BIGINT      NOT NULL DEFAULT 0,
    -- revision sepetin şekil sayacıdır; toplamları etkileyen her yapısal
    -- değişiklikte artar.
    revision        BIGINT      NOT NULL DEFAULT 0,
    -- totals_revision toplamların hangi şekil için hesaplandığını damgalar.
    totals_revision BIGINT      NOT NULL DEFAULT 0,
    metadata        JSONB       NOT NULL DEFAULT '{}'::jsonb,
    -- completed_at dolu ise sepet DEĞİŞTİRİLEMEZ; sipariş geçmişinin dayandığı
    -- kayıt odur.
    completed_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at      TIMESTAMPTZ,

    CONSTRAINT carts_subtotal_nonneg       CHECK (subtotal >= 0),
    CONSTRAINT carts_discount_total_nonneg CHECK (discount_total >= 0),
    CONSTRAINT carts_tax_total_nonneg      CHECK (tax_total >= 0),
    CONSTRAINT carts_shipping_total_nonneg CHECK (shipping_total >= 0),
    CONSTRAINT carts_total_nonneg          CHECK (total >= 0),
    CONSTRAINT carts_totals_consistent
        CHECK (total = subtotal - discount_total + tax_total + shipping_total),
    CONSTRAINT carts_revision_nonneg       CHECK (revision >= 0),
    -- Toplamlar HENÜZ OLMAYAN bir şekil için damgalanamaz.
    CONSTRAINT carts_totals_revision_range
        CHECK (totals_revision >= 0 AND totals_revision <= revision)
);

CREATE INDEX IF NOT EXISTS carts_alive_idx
    ON carts (created_at DESC, id DESC)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS carts_customer_idx
    ON carts (customer_id)
    WHERE customer_id IS NOT NULL AND deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS carts_region_idx
    ON carts (region_id)
    WHERE deleted_at IS NULL;

-- cart_line_items sepetteki bir satırdır.
--
-- variant_id product modülünün kimliğidir ve FOREIGN KEY DEĞİLDİR (Prensip
-- 2.2): sepet, katalog silinse bile kendi geçmişini taşımaya devam eder.
-- Bu yüzden title ve unit_price de KOPYALANIR; varyantın adı sonradan
-- değişse de sepette görülen ad değişmez.
CREATE TABLE IF NOT EXISTS cart_line_items (
    id             TEXT        PRIMARY KEY,
    cart_id        TEXT        NOT NULL REFERENCES carts (id) ON DELETE CASCADE,
    -- variant_id product modülünün kimliğidir; FK YOKTUR (Prensip 2.2).
    variant_id     TEXT        NOT NULL,
    title          TEXT        NOT NULL,
    quantity       BIGINT      NOT NULL,
    unit_price     BIGINT      NOT NULL DEFAULT 0,
    subtotal       BIGINT      NOT NULL DEFAULT 0,
    discount_total BIGINT      NOT NULL DEFAULT 0,
    tax_total      BIGINT      NOT NULL DEFAULT 0,
    total          BIGINT      NOT NULL DEFAULT 0,
    metadata       JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at     TIMESTAMPTZ,

    CONSTRAINT cart_line_items_quantity_positive     CHECK (quantity > 0),
    CONSTRAINT cart_line_items_unit_price_nonneg     CHECK (unit_price >= 0),
    CONSTRAINT cart_line_items_subtotal_nonneg       CHECK (subtotal >= 0),
    CONSTRAINT cart_line_items_discount_total_nonneg CHECK (discount_total >= 0),
    CONSTRAINT cart_line_items_tax_total_nonneg      CHECK (tax_total >= 0),
    CONSTRAINT cart_line_items_total_nonneg          CHECK (total >= 0),
    CONSTRAINT cart_line_items_totals_consistent
        CHECK (total = subtotal - discount_total + tax_total)
);

-- Bir varyant bir sepette EN FAZLA BİR satırda bulunur: aynı varyant ikinci kez
-- eklendiğinde yeni satır açılmaz, var olan satırın adedi artar (bkz.
-- service.AddLineItem). Kısıt bu kararın yapısal karşılığıdır — eşzamanlı iki
-- ekleme sepetin satır kilidini atlatsa bile ikinci satır açılamaz.
CREATE UNIQUE INDEX IF NOT EXISTS cart_line_items_cart_variant_uniq
    ON cart_line_items (cart_id, variant_id)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS cart_line_items_cart_idx
    ON cart_line_items (cart_id, created_at, id)
    WHERE deleted_at IS NULL;

-- cart_addresses sepetin kargo/fatura adresidir.
--
-- Adres, customer modülündeki adresten KOPYALANIR ve sepet kendi kopyasını
-- tutar. Müşteri adres defterindeki kaydını sonradan değiştirdiğinde ya da
-- sildiğinde geçmiş sepet (ve ondan doğan sipariş) bozulmaz. source_address_id
-- yalnızca kökeni belgeler; FOREIGN KEY DEĞİLDİR ve okuma için kullanılmaz.
CREATE TABLE IF NOT EXISTS cart_addresses (
    id                TEXT        PRIMARY KEY,
    cart_id           TEXT        NOT NULL REFERENCES carts (id) ON DELETE CASCADE,
    -- address_type 'shipping' ya da 'billing'dir.
    address_type      TEXT        NOT NULL,
    -- source_address_id kopyalandığı customer adresinin kimliğidir; FK YOKTUR.
    source_address_id TEXT,
    first_name        TEXT,
    last_name         TEXT,
    company           TEXT,
    address_1         TEXT,
    address_2         TEXT,
    city              TEXT,
    province          TEXT,
    postal_code       TEXT,
    country_code      TEXT,
    phone             TEXT,
    metadata          JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at        TIMESTAMPTZ,

    CONSTRAINT cart_addresses_type_valid
        CHECK (address_type IN ('shipping', 'billing'))
);

-- Sepet başına her türden EN FAZLA BİR yaşayan adres bulunur.
CREATE UNIQUE INDEX IF NOT EXISTS cart_addresses_cart_type_uniq
    ON cart_addresses (cart_id, address_type)
    WHERE deleted_at IS NULL;

-- cart_shipping_methods sepete seçilmiş kargo yöntemidir.
--
-- shipping_option_id fulfillment modülünün kimliğidir (Faz 7) ve FOREIGN KEY
-- DEĞİLDİR; Faz 5'te seçenek kataloğu henüz yoktur, bu yüzden NULL olabilir.
-- amount minor unit'tir ve sepetin shipping_total'ına workflow tarafından
-- toplanır; bu tablo toplamı kendi yazmaz.
CREATE TABLE IF NOT EXISTS cart_shipping_methods (
    id                 TEXT        PRIMARY KEY,
    cart_id            TEXT        NOT NULL REFERENCES carts (id) ON DELETE CASCADE,
    name               TEXT        NOT NULL,
    -- shipping_option_id fulfillment modülünün kimliğidir; FK YOKTUR.
    shipping_option_id TEXT,
    amount             BIGINT      NOT NULL DEFAULT 0,
    data               JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at         TIMESTAMPTZ,

    CONSTRAINT cart_shipping_methods_amount_nonneg CHECK (amount >= 0)
);

-- Aynı kargo seçeneği bir sepete iki kez eklenemez. Seçeneksiz (NULL) yöntemler
-- kısıtın dışındadır: NULL'lar benzersiz indekste birbirine eşit sayılmaz ve
-- Faz 7'ye kadar tüm yöntemler seçeneksizdir.
CREATE UNIQUE INDEX IF NOT EXISTS cart_shipping_methods_cart_option_uniq
    ON cart_shipping_methods (cart_id, shipping_option_id)
    WHERE shipping_option_id IS NOT NULL AND deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS cart_shipping_methods_cart_idx
    ON cart_shipping_methods (cart_id, created_at, id)
    WHERE deleted_at IS NULL;
