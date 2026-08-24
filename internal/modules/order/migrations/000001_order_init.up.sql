-- order modülünün şeması (plan Faz 6).
--
-- Sahiplik: buradaki altı tablo YALNIZCA order modülüne aittir. Modül İÇİ
-- foreign key'ler serbesttir ve kullanılır (satırlar, özet, iade/değişim/hasar
-- kayıtları siparişe ON DELETE CASCADE ile bağlıdır); başka bir modülün
-- tablosuna REFERENCES VERİLMEZ (Prensip 2.2 — cross-module FK yasağı).
-- Bu yüzden orders.region_id, orders.customer_id, orders.cart_id ve
-- order_line_items.variant_id serbest METİNDİR: ilişki Module Links üzerinden
-- kurulur.
--
-- Para: TÜM tutarlar BIGINT ve minor unit'tir (kuruş/cent); para birimi ayrı
-- sütunda durur (plan Bölüm 8). Kayan nokta hiçbir yerde kullanılmaz.
--
-- Zaman: tüm damgalar timestamptz (UTC). Silme yumuşaktır (deleted_at) ve
-- orders/order_line_items okuma sorguları deleted_at IS NULL süzer. Faz 6'da
-- siparişi SİLEN bir yüzey yoktur.
--
-- DİKKAT — order_summaries BU KURALIN DIŞINDADIR: tabloda deleted_at sütunu
-- YOKTUR ve GetOrderSummary siparişin canlılığını hiç sorgulamaz. Bugün zararsız
-- olmasının tek sebebi silen bir yüzeyin bulunmamasıdır. Siparişe yumuşak silme
-- GELDİĞİNDE bu sorgu da bağlanmalıdır (JOIN orders + deleted_at IS NULL),
-- aksi hâlde GetOrder NotFound derken GetOrderSummary dolu kayıt döner.

-- orders bir siparişdir.
--
-- # display_id neden veritabanı üretir
--
-- display_id müşteriye gösterilen, insan okunur ARTAN sayıdır ("1042 numaralı
-- siparişiniz"). Uygulama katmanında "en büyüğü oku, bir ekle, yaz" ile
-- üretilseydi iki eşzamanlı sipariş AYNI numarayı alırdı: okuma ile yazma
-- arasında ikinci işlem araya girer ve iki satır da aynı MAX+1 değerini
-- hesaplardı. Satır kilidi de yetmezdi — kilitlenecek ORTAK bir satır yoktur,
-- ikisi de YENİ satır açar.
--
-- Bu yüzden numarayı IDENTITY sütunu (yani bir sequence) üretir. Sequence
-- işlemin dışında, atomik olarak ilerler; iki eşzamanlı INSERT birbirinin
-- değerini göremez ve aynı sayıyı alamaz. GENERATED ALWAYS seçilmiştir:
-- BY DEFAULT olsaydı bir INSERT sütunu açıkça yazıp sequence'ı atlayabilir ve
-- garantiyi delerdi. orders_display_id_uniq ise son savunmadır — sequence
-- elle geri sarılsa (setval) bile çakışma satır yazılmadan yakalanır.
--
-- Sequence sütunla birlikte doğar ve DROP TABLE ile birlikte düşer; ayrı bir
-- CREATE/DROP SEQUENCE gerekmez.
--
-- # Toplam alanları
--
-- Sipariş toplamları sepetin ANLIK GÖRÜNTÜSÜNDEN kopyalanır ve bir daha
-- değişmez: sipariş, "o an ne satıldı ve ne kadar tutardı" sorusunun kalıcı
-- yanıtıdır. Kimlik kısıtı (orders_totals_consistent) bir saga adımının yanlış
-- hesabının sessizce siparişe yazılmasını engeller; servis aynı kontrolü daha
-- okunabilir bir hatayla önce yapar, buradaki kısıt son savunmadır ve doğrudan
-- SQL ile yapılan müdahaleyi de kapsar.
CREATE TABLE IF NOT EXISTS orders (
    id              TEXT        PRIMARY KEY,
    -- display_id müşteriye gösterilen, insan okunur ARTAN numaradır.
    display_id      BIGINT      NOT NULL GENERATED ALWAYS AS IDENTITY,
    -- status siparişin yaşam döngüsündeki yeridir.
    status          TEXT        NOT NULL DEFAULT 'pending',
    -- region_id region modülünün kimliğidir; FK YOKTUR (Prensip 2.2).
    region_id       TEXT        NOT NULL,
    -- customer_id customer modülünün kimliğidir; NULL ise sipariş MİSAFİRE aittir.
    customer_id     TEXT,
    email           TEXT,
    -- currency_code ISO 4217 kodudur ve BÜYÜK harf saklanır.
    currency_code   TEXT        NOT NULL,
    -- cart_id siparişin doğduğu sepettir; cart modülünün kimliğidir ve FK
    -- DEĞİLDİR. Yalnızca KÖKENİ belgeler; okuma için kullanılmaz.
    cart_id         TEXT,
    -- idempotency_key aynı siparişin iki kez yazılmasını engeller (Prensip 2.6).
    -- Saga bir adımı yeniden deneyebilir; anahtar olmadan tekrar, müşteriye
    -- İKİNCİ BİR SİPARİŞ açmak demek olurdu.
    idempotency_key TEXT,
    subtotal        BIGINT      NOT NULL DEFAULT 0,
    discount_total  BIGINT      NOT NULL DEFAULT 0,
    tax_total       BIGINT      NOT NULL DEFAULT 0,
    shipping_total  BIGINT      NOT NULL DEFAULT 0,
    total           BIGINT      NOT NULL DEFAULT 0,
    metadata        JSONB       NOT NULL DEFAULT '{}'::jsonb,
    -- placed_at siparişin verildiği andır; sipariş kaydının doğum anıdır.
    placed_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at    TIMESTAMPTZ,
    canceled_at     TIMESTAMPTZ,
    cancel_reason   TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at      TIMESTAMPTZ,

    CONSTRAINT orders_status_valid
        CHECK (status IN ('pending', 'completed', 'archived', 'canceled')),
    CONSTRAINT orders_subtotal_nonneg       CHECK (subtotal >= 0),
    CONSTRAINT orders_discount_total_nonneg CHECK (discount_total >= 0),
    CONSTRAINT orders_tax_total_nonneg      CHECK (tax_total >= 0),
    CONSTRAINT orders_shipping_total_nonneg CHECK (shipping_total >= 0),
    CONSTRAINT orders_total_nonneg          CHECK (total >= 0),
    CONSTRAINT orders_totals_consistent
        CHECK (total = subtotal - discount_total + tax_total + shipping_total),
    -- İndirim ara toplamı AŞAMAZ: aşsaydı müşteri satın aldığı maldan fazlasını
    -- geri kazanır, vergi ve kargo indirimle karşılanırdı.
    CONSTRAINT orders_discount_within_subtotal
        CHECK (discount_total <= subtotal),
    -- Durum ile damga birbirinin AYNASIDIR; ikisinin ayrışması, iptal edilmiş
    -- görünen ama iptal anı olmayan (ya da tersi) bir kayıt demekti.
    CONSTRAINT orders_canceled_stamp
        CHECK ((status = 'canceled') = (canceled_at IS NOT NULL)),
    CONSTRAINT orders_completed_stamp
        CHECK ((status IN ('completed', 'archived')) = (completed_at IS NOT NULL))
);

-- display_id benzersizliği SON SAVUNMADIR: sequence zaten çakışmaz, ama
-- sequence elle geri sarılırsa (setval) ya da bir kayıt kopyalanırsa aynı
-- numara ikinci kez yazılamaz.
CREATE UNIQUE INDEX IF NOT EXISTS orders_display_id_uniq
    ON orders (display_id);

CREATE INDEX IF NOT EXISTS orders_alive_idx
    ON orders (created_at DESC, id DESC)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS orders_customer_idx
    ON orders (customer_id)
    WHERE customer_id IS NOT NULL AND deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS orders_region_idx
    ON orders (region_id)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS orders_status_idx
    ON orders (status)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS orders_cart_idx
    ON orders (cart_id)
    WHERE cart_id IS NOT NULL AND deleted_at IS NULL;

-- Aynı idempotency anahtarıyla İKİNCİ bir sipariş açılamaz. Yumuşak silinmiş
-- sipariş kısıtın dışındadır: silinen bir siparişin anahtarı yeniden
-- kullanılabilmelidir, aksi hâlde silme işlemi anahtarı sonsuza dek tüketirdi.
CREATE UNIQUE INDEX IF NOT EXISTS orders_idempotency_key_uniq
    ON orders (idempotency_key)
    WHERE idempotency_key IS NOT NULL AND deleted_at IS NULL;

-- order_line_items siparişteki bir satırdır.
--
-- variant_id product modülünün kimliğidir ve FOREIGN KEY DEĞİLDİR (Prensip
-- 2.2). title ve unit_price de KOPYALANIR: katalog sonradan değişse (ya da
-- varyant silinse) bile faturada görülen ad ve tutar değişmez.
--
-- (order_id, variant_id) üzerinde benzersizlik kısıtı BİLİNÇLİ OLARAK YOKTUR
-- (sepette vardır). Sipariş tarihsel bir kayıttır ve sonraki fazlarda değişim
-- (exchange) aynı varyant için ikinci bir satır ekleyebilir; sepetteki "aynı
-- varyant tek satır" kuralı ise sepetin düzenlenebilir olmasından doğar.
-- Aynı siparişin iki kez yazılmasına karşı koruma satır düzeyinde değil,
-- orders_idempotency_key_uniq ile sipariş düzeyindedir.
CREATE TABLE IF NOT EXISTS order_line_items (
    id             TEXT        PRIMARY KEY,
    order_id       TEXT        NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
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

    CONSTRAINT order_line_items_quantity_positive     CHECK (quantity > 0),
    CONSTRAINT order_line_items_unit_price_nonneg     CHECK (unit_price >= 0),
    CONSTRAINT order_line_items_subtotal_nonneg       CHECK (subtotal >= 0),
    CONSTRAINT order_line_items_discount_total_nonneg CHECK (discount_total >= 0),
    CONSTRAINT order_line_items_tax_total_nonneg      CHECK (tax_total >= 0),
    CONSTRAINT order_line_items_total_nonneg          CHECK (total >= 0),
    -- Kargo satır düzeyinde yoktur; kargo siparişin tamamına aittir.
    CONSTRAINT order_line_items_totals_consistent
        CHECK (total = subtotal - discount_total + tax_total),
    CONSTRAINT order_line_items_discount_within_subtotal
        CHECK (discount_total <= subtotal)
);

CREATE INDEX IF NOT EXISTS order_line_items_order_idx
    ON order_line_items (order_id, created_at, id)
    WHERE deleted_at IS NULL;

-- order_summaries siparişin ödenen/iade edilen/kalan tutar özetidir.
--
-- Sipariş başına TEK satırdır ve siparişle birlikte sıfırlanmış olarak doğar:
-- "özeti olmayan sipariş" diye bir durum yoktur, dolayısıyla okuyan taraf
-- NULL ile sıfırı ayırt etmek zorunda kalmaz.
--
-- KALAN tutar sütun olarak SAKLANMAZ; total - (paid_total - refunded_total)
-- olarak okunur (bkz. models.OrderSummary.Outstanding). Saklansaydı üç
-- sütunun birbiriyle tutarlılığını ayrı bir kısıtla korumak gerekirdi ve
-- türetilmiş bir değerin bayatlaması mümkün olurdu.
--
-- Ödeme tutarını YAZAN taraf payment modülü DEĞİLDİR: iki modül birbirini
-- tanımaz (Prensip 2.1). Yazma, ödeme sonucunu bilen workflow ya da bir olay
-- abonesi üzerinden bu modülün servisine gelir.
CREATE TABLE IF NOT EXISTS order_summaries (
    id             TEXT        PRIMARY KEY,
    order_id       TEXT        NOT NULL UNIQUE REFERENCES orders (id) ON DELETE CASCADE,
    -- paid_total siparişe karşılık TAHSİL EDİLEN toplam tutardır (minor unit).
    paid_total     BIGINT      NOT NULL DEFAULT 0,
    -- refunded_total müşteriye GERİ ÖDENEN toplam tutardır (minor unit).
    refunded_total BIGINT      NOT NULL DEFAULT 0,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT order_summaries_paid_total_nonneg     CHECK (paid_total >= 0),
    CONSTRAINT order_summaries_refunded_total_nonneg CHECK (refunded_total >= 0),
    -- Tahsil edilmemiş bir tutar iade EDİLEMEZ.
    CONSTRAINT order_summaries_refund_within_paid
        CHECK (refunded_total <= paid_total)
);

-- order_returns bir iade kaydının İSKELETİDİR (plan Bölüm 6).
--
-- Faz 6 yalnızca kaydı ve temel CRUD'ını kurar; iade iş akışı (satır bazlı
-- iade, stok geri alma, ödeme iadesi) sonraki fazlara aittir. Bu yüzden satır
-- bazlı çocuk tablosu HENÜZ YOKTUR: iş akışı yazılmadan tasarlanan bir çocuk
-- şeması, akış geldiğinde büyük olasılıkla değişecek ve geri alınması gereken
-- bir migration bırakacaktı.
CREATE TABLE IF NOT EXISTS order_returns (
    id            TEXT        PRIMARY KEY,
    order_id      TEXT        NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    status        TEXT        NOT NULL DEFAULT 'requested',
    -- refund_amount iade edilmesi planlanan tutardır (minor unit).
    refund_amount BIGINT      NOT NULL DEFAULT 0,
    reason        TEXT,
    note          TEXT,
    metadata      JSONB       NOT NULL DEFAULT '{}'::jsonb,
    received_at   TIMESTAMPTZ,
    canceled_at   TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at    TIMESTAMPTZ,

    CONSTRAINT order_returns_status_valid
        CHECK (status IN ('requested', 'received', 'canceled')),
    CONSTRAINT order_returns_refund_amount_nonneg CHECK (refund_amount >= 0)
);

CREATE INDEX IF NOT EXISTS order_returns_order_idx
    ON order_returns (order_id, created_at DESC, id DESC)
    WHERE deleted_at IS NULL;

-- order_exchanges bir değişim kaydının İSKELETİDİR (plan Bölüm 6).
--
-- difference_due NEGATİF OLABİLİR ve bu yüzden nonneg kısıtı YOKTUR: değişimde
-- fark müşteriden tahsil edilebileceği gibi müşteriye de ödenebilir. Tutar yine
-- TAM SAYI minor unit'tir (plan Bölüm 8); işaret yönü belirtir, ölçeği değil.
CREATE TABLE IF NOT EXISTS order_exchanges (
    id             TEXT        PRIMARY KEY,
    order_id       TEXT        NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    status         TEXT        NOT NULL DEFAULT 'requested',
    -- difference_due pozitifse müşteri öder, negatifse müşteriye ödenir.
    difference_due BIGINT      NOT NULL DEFAULT 0,
    note           TEXT,
    metadata       JSONB       NOT NULL DEFAULT '{}'::jsonb,
    completed_at   TIMESTAMPTZ,
    canceled_at    TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at     TIMESTAMPTZ,

    CONSTRAINT order_exchanges_status_valid
        CHECK (status IN ('requested', 'completed', 'canceled'))
);

CREATE INDEX IF NOT EXISTS order_exchanges_order_idx
    ON order_exchanges (order_id, created_at DESC, id DESC)
    WHERE deleted_at IS NULL;

-- order_claims bir hasar/eksik kaydının İSKELETİDİR (plan Bölüm 6).
--
-- claim_type talebin nasıl karşılanacağını söyler: 'refund' para iadesi,
-- 'replace' ürünün yenisiyle değiştirilmesi.
CREATE TABLE IF NOT EXISTS order_claims (
    id            TEXT        PRIMARY KEY,
    order_id      TEXT        NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    claim_type    TEXT        NOT NULL,
    status        TEXT        NOT NULL DEFAULT 'requested',
    -- refund_amount claim_type = 'refund' iken iade edilecek tutardır.
    refund_amount BIGINT      NOT NULL DEFAULT 0,
    reason        TEXT,
    note          TEXT,
    metadata      JSONB       NOT NULL DEFAULT '{}'::jsonb,
    completed_at  TIMESTAMPTZ,
    canceled_at   TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at    TIMESTAMPTZ,

    CONSTRAINT order_claims_type_valid
        CHECK (claim_type IN ('refund', 'replace')),
    CONSTRAINT order_claims_status_valid
        CHECK (status IN ('requested', 'completed', 'canceled')),
    CONSTRAINT order_claims_refund_amount_nonneg CHECK (refund_amount >= 0)
);

CREATE INDEX IF NOT EXISTS order_claims_order_idx
    ON order_claims (order_id, created_at DESC, id DESC)
    WHERE deleted_at IS NULL;
