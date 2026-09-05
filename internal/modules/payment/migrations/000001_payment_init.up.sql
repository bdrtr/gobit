-- payment modülünün şeması (plan Faz 6).
--
-- Sahiplik: buradaki beş tablo YALNIZCA payment modülüne aittir. Modül içi
-- foreign key'ler serbesttir ve kullanılır; başka bir modülün tablosuna
-- REFERENCES VERİLMEZ (Prensip 2.2 — cross-module FK yasağı). Bu yüzden
-- payment_collections.reference (sepet ya da sipariş kimliği) FK DEĞİLDİR:
-- ilişki Module Links üzerinden kurulur ve link tablosu çekirdektedir.
--
-- Para: tüm tutarlar BIGINT ve minor unit'tir (kuruş/cent); para birimi AYRI
-- sütunda durur (plan Bölüm 8). NUMERIC ya da kayan nokta hiçbir yerde
-- kullanılmaz — tahsilat ile mutabakat arasındaki tek kuruşluk fark, ancak
-- gün sonunda ve elle bulunur.
--
-- Zaman: tüm damgalar timestamptz (UTC). Silme yumuşaktır (deleted_at) ve tüm
-- okuma sorguları deleted_at IS NULL filtresi uygular.

-- payment_collections bir sepet/sipariş için toplanan ödemelerin kabıdır.
--
-- Durum SAKLANIR ama TÜRETİLİR: servis her mutasyondan sonra tutarlardan ve
-- oturum sayımlarından yeniden hesaplayıp yazar (bkz. service.CollectionStatusFor).
-- Sütun, sorgulanabilirlik için vardır; gerçeğin kaynağı tutarlardır.
CREATE TABLE IF NOT EXISTS payment_collections (
    id                TEXT        PRIMARY KEY,
    -- reference çağıranın kendi kaydının kimliğidir (sepet ya da sipariş).
    -- FK YOKTUR (Prensip 2.2); bağ Module Links ile kurulur.
    reference         TEXT        NOT NULL,
    amount            BIGINT      NOT NULL,
    currency_code     TEXT        NOT NULL,
    status            TEXT        NOT NULL DEFAULT 'not_paid',
    -- Aşağıdaki üç tutar oturum/tahsilat/iade kayıtlarının toplamıdır ve
    -- koleksiyon satırının kilidi altında güncellenir.
    authorized_amount BIGINT      NOT NULL DEFAULT 0,
    captured_amount   BIGINT      NOT NULL DEFAULT 0,
    refunded_amount   BIGINT      NOT NULL DEFAULT 0,
    metadata          JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at        TIMESTAMPTZ,

    CONSTRAINT payment_collections_amount_positive     CHECK (amount > 0),
    CONSTRAINT payment_collections_authorized_nonneg   CHECK (authorized_amount >= 0),
    CONSTRAINT payment_collections_captured_nonneg     CHECK (captured_amount >= 0),
    CONSTRAINT payment_collections_refunded_nonneg     CHECK (refunded_amount >= 0),
    -- İade edilen tutar tahsil edileni AŞAMAZ. Servis bunu zaten reddeder;
    -- buradaki kısıt son savunmadır: doğrudan SQL ile yapılan bir müdahale de
    -- olmayan parayı iade edemez.
    CONSTRAINT payment_collections_refund_le_capture   CHECK (refunded_amount <= captured_amount),
    -- Bloke edilen ve tahsil edilen tutar koleksiyonun TUTARINI aşamaz.
    -- Koleksiyon, toplanacak paranın tavanıdır: aşan bir toplam, müşteriden
    -- siparişten fazlasının alınması demektir. Servis bunu zaten reddeder
    -- (açık oturumların rezerve ettiği tutar da düşülür); buradaki kısıt son
    -- savunmadır ve doğrudan SQL ile yapılan bir müdahaleyi de durdurur.
    CONSTRAINT payment_collections_authorized_le_amount CHECK (authorized_amount <= amount),
    CONSTRAINT payment_collections_captured_le_amount   CHECK (captured_amount <= amount),
    CONSTRAINT payment_collections_currency_format     CHECK (currency_code ~ '^[A-Z]{3}$'),
    CONSTRAINT payment_collections_status_valid
        CHECK (status IN ('not_paid', 'awaiting', 'authorized',
                          'partially_captured', 'captured',
                          'partially_refunded', 'refunded', 'canceled'))
);

CREATE INDEX IF NOT EXISTS payment_collections_reference_idx
    ON payment_collections (reference)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS payment_collections_alive_idx
    ON payment_collections (created_at DESC, id DESC)
    WHERE deleted_at IS NULL;

-- payment_sessions bir SAĞLAYICIDA açılmış ödeme oturumudur.
--
-- external_id sağlayıcının kendi kimliğidir; mutabakatta iki sistemi
-- eşleştiren alan budur. data, sağlayıcının döndürdüğü ham gövdedir ve
-- yorumlanmadan saklanır.
CREATE TABLE IF NOT EXISTS payment_sessions (
    id                    TEXT        PRIMARY KEY,
    payment_collection_id TEXT        NOT NULL REFERENCES payment_collections (id) ON DELETE CASCADE,
    provider_id           TEXT        NOT NULL,
    external_id           TEXT        NOT NULL,
    status                TEXT        NOT NULL DEFAULT 'pending',
    amount                BIGINT      NOT NULL,
    authorized_amount     BIGINT      NOT NULL DEFAULT 0,
    currency_code         TEXT        NOT NULL,
    data                  JSONB       NOT NULL DEFAULT '{}'::jsonb,
    -- idempotency_key aynı oturumun iki kez açılmasını engeller (plan Bölüm 2.6).
    idempotency_key       TEXT        NOT NULL,
    -- decline_reason yalnızca status = 'failed' iken doludur; teşhis içindir,
    -- müşteriye gösterilmek üzere değildir.
    decline_reason        TEXT,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at            TIMESTAMPTZ,

    CONSTRAINT payment_sessions_amount_positive   CHECK (amount > 0),
    CONSTRAINT payment_sessions_authorized_nonneg CHECK (authorized_amount >= 0),
    CONSTRAINT payment_sessions_authorized_le_amount CHECK (authorized_amount <= amount),
    CONSTRAINT payment_sessions_currency_format   CHECK (currency_code ~ '^[A-Z]{3}$'),
    CONSTRAINT payment_sessions_status_valid
        CHECK (status IN ('pending', 'authorized', 'captured', 'canceled', 'failed'))
);

-- Bir (sağlayıcı, idempotency anahtarı) çifti YAŞAYAN oturumlar arasında
-- tektir. Saga bir adımı yeniden denediğinde ikinci CreateSession bu indekse
-- takılmadan ÖNCE mevcut oturumu bulur; indeks son savunmadır ve iki eşzamanlı
-- açmadan yalnızca birinin satır yazmasını garanti eder.
CREATE UNIQUE INDEX IF NOT EXISTS payment_sessions_provider_idempotency_uniq
    ON payment_sessions (provider_id, idempotency_key)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS payment_sessions_collection_idx
    ON payment_sessions (payment_collection_id, created_at DESC)
    WHERE deleted_at IS NULL;

-- payments gerçekleşmiş tahsilattır.
--
-- Bir oturumdan EN FAZLA BİR tahsilat çıkar; kısmi tahsilat oturumu kapatır.
-- Benzersiz indeks bunu zorlar ve Capture'ı idempotent yapan şey odur: ikinci
-- çağrı yeni satır yazmaz, var olanı döner.
CREATE TABLE IF NOT EXISTS payments (
    id                    TEXT        PRIMARY KEY,
    payment_session_id    TEXT        NOT NULL REFERENCES payment_sessions (id) ON DELETE CASCADE,
    payment_collection_id TEXT        NOT NULL REFERENCES payment_collections (id) ON DELETE CASCADE,
    amount                BIGINT      NOT NULL,
    currency_code         TEXT        NOT NULL,
    refunded_amount       BIGINT      NOT NULL DEFAULT 0,
    captured_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at            TIMESTAMPTZ,

    CONSTRAINT payments_amount_positive    CHECK (amount > 0),
    CONSTRAINT payments_refunded_nonneg    CHECK (refunded_amount >= 0),
    CONSTRAINT payments_refund_le_amount   CHECK (refunded_amount <= amount),
    CONSTRAINT payments_currency_format    CHECK (currency_code ~ '^[A-Z]{3}$')
);

CREATE UNIQUE INDEX IF NOT EXISTS payments_session_uniq
    ON payments (payment_session_id)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS payments_collection_idx
    ON payments (payment_collection_id, created_at DESC)
    WHERE deleted_at IS NULL;

-- refunds bir tahsilatın geri ödenmesidir. Kısmi iade birden çok satır üretir;
-- toplamları payments.refunded_amount sütununda tutulur.
CREATE TABLE IF NOT EXISTS refunds (
    id         TEXT        PRIMARY KEY,
    payment_id TEXT        NOT NULL REFERENCES payments (id) ON DELETE CASCADE,
    amount     BIGINT      NOT NULL,
    reason     TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ,

    CONSTRAINT refunds_amount_positive CHECK (amount > 0)
);

CREATE INDEX IF NOT EXISTS refunds_payment_idx
    ON refunds (payment_id, created_at DESC)
    WHERE deleted_at IS NULL;

-- payment_manual_sessions MANUEL sağlayıcının kendi defteridir.
--
-- Neden AYRI bir tablo: manual sağlayıcı, gerçek bir ödeme kuruluşunu TAKLİT
-- eder. Gerçek sağlayıcının durumu kendi sistemindedir ve modül ona yalnızca
-- PaymentProvider arayüzünden ulaşır. Aynı ayrımın burada da korunması, modülün
-- kazara sağlayıcının iç durumunu okumasını yapısal olarak engeller: payment
-- servisi bu tabloya HİÇ dokunmaz.
--
-- Neden BELLEKTE DEĞİL: e2e akışları ve Faz 9 yük testi süreç yeniden
-- başladığında açılmış bir oturumu bulabilmelidir. Bellekte tutulan bir
-- oturum, sunucunun her yeniden başlatılışında "oturum bulunamadı" üretirdi ve
-- saga'nın telafi adımı (Cancel) tam da sürecin düştüğü senaryoda çalışamazdı.
--
-- Yumuşak silme YOKTUR: bu tablo modülün alan verisi değil, taklit edilen dış
-- sistemin defteridir; kayıtları hiç silinmez.
CREATE TABLE IF NOT EXISTS payment_manual_sessions (
    id                TEXT        PRIMARY KEY,
    idempotency_key   TEXT        NOT NULL,
    reference         TEXT        NOT NULL,
    amount            BIGINT      NOT NULL,
    currency_code     TEXT        NOT NULL,
    status            TEXT        NOT NULL DEFAULT 'pending',
    authorized_amount BIGINT      NOT NULL DEFAULT 0,
    captured_amount   BIGINT      NOT NULL DEFAULT 0,
    refunded_amount   BIGINT      NOT NULL DEFAULT 0,
    data              JSONB       NOT NULL DEFAULT '{}'::jsonb,
    decline_reason    TEXT,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT payment_manual_sessions_amount_positive    CHECK (amount > 0),
    CONSTRAINT payment_manual_sessions_authorized_nonneg  CHECK (authorized_amount >= 0),
    CONSTRAINT payment_manual_sessions_captured_nonneg    CHECK (captured_amount >= 0),
    CONSTRAINT payment_manual_sessions_refunded_nonneg    CHECK (refunded_amount >= 0),
    CONSTRAINT payment_manual_sessions_captured_le_auth   CHECK (captured_amount <= authorized_amount),
    CONSTRAINT payment_manual_sessions_refund_le_capture  CHECK (refunded_amount <= captured_amount),
    CONSTRAINT payment_manual_sessions_status_valid
        CHECK (status IN ('pending', 'authorized', 'captured', 'canceled', 'failed'))
);

-- Aynı idempotency anahtarı İKİNCİ bir oturum açamaz. Sağlayıcı sözleşmesinin
-- (core/provider) idempotency şartını nihai olarak zorlayan kısıt budur.
CREATE UNIQUE INDEX IF NOT EXISTS payment_manual_sessions_idempotency_uniq
    ON payment_manual_sessions (idempotency_key);
