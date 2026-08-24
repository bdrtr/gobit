-- promotion modülünün şeması (plan Faz 7, Bölüm 6).
--
-- Tablolar YALNIZCA bu modüle aittir. Prensip 2.2 gereği başka bir modülün
-- tablosuna REFERENCES verilmez: bir kullanımın hangi siparişe ait olduğu
-- promotion_redemption.reference sütununda SERBEST metin olarak durur ve
-- foreign key değildir. Modülün KENDİ tabloları arasındaki foreign key'ler ise
-- serbesttir ve kullanılır.
--
-- Zaman sütunları TIMESTAMPTZ'dir ve daima UTC yazılır; silme SOFT'tur
-- (deleted_at) ve tüm okuma sorguları deleted_at IS NULL filtresi uygular.
--
-- Para TAM SAYI minor unit'tir, oranlar BAZ PUANDIR (10000 = %100); float
-- hiçbir sütunda kullanılmaz (plan Bölüm 8).

-- campaign promosyonların kabıdır: ortak tarih penceresi ve ortak bütçe.
--
-- Bütçe iki birimden birinde ölçülür: "spend" (para, minor unit) ya da "usage"
-- (adet). Para birimi YALNIZCA "spend"te vardır ve orada ZORUNLUDUR; bütçesiz
-- bir kampanyada sınır da olamaz. Üç kısıt bu üç kuralı veritabanı düzeyinde
-- kilitler, böylece doğrudan SQL çalıştıran bir bakım betiği bile tutarsız bir
-- bütçe bırakamaz.
CREATE TABLE IF NOT EXISTS campaign (
    id                   TEXT PRIMARY KEY,
    name                 TEXT        NOT NULL,
    campaign_identifier  TEXT        NOT NULL,
    description          TEXT        NOT NULL DEFAULT '',
    starts_at            TIMESTAMPTZ,
    ends_at              TIMESTAMPTZ,
    budget_type          TEXT        NOT NULL DEFAULT 'none',
    budget_limit         BIGINT,
    budget_used          BIGINT      NOT NULL DEFAULT 0,
    budget_currency_code TEXT,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at           TIMESTAMPTZ,
    CONSTRAINT campaign_name_check       CHECK (name <> ''),
    CONSTRAINT campaign_identifier_check CHECK (campaign_identifier <> ''),
    CONSTRAINT campaign_window_check     CHECK (starts_at IS NULL OR ends_at IS NULL OR starts_at < ends_at),
    CONSTRAINT campaign_budget_type_check CHECK (budget_type IN ('none', 'spend', 'usage')),
    CONSTRAINT campaign_budget_limit_check CHECK (
        budget_limit IS NULL OR (budget_limit >= 0 AND budget_limit <= 1000000000000)
    ),
    CONSTRAINT campaign_budget_used_check CHECK (budget_used >= 0),
    -- CASE kullanılır, "A OR B" DEĞİL: SQL'de NULL ile karşılaştırma NULL üretir
    -- ve CHECK kısıtı NULL sonucu BAŞARILI sayar. "budget_type = 'spend' AND
    -- budget_currency_code ~ '...'" biçimindeki bir kısıt, para birimi NULL
    -- olduğunda NULL döner ve tutarsız satırı SESSİZCE kabul ederdi.
    CONSTRAINT campaign_budget_currency_check CHECK (
        CASE budget_type
            WHEN 'spend' THEN budget_currency_code IS NOT NULL
                              AND budget_currency_code ~ '^[A-Z]{3}$'
            ELSE budget_currency_code IS NULL
        END
    ),
    CONSTRAINT campaign_budget_none_check CHECK (budget_type <> 'none' OR budget_limit IS NULL)
);

-- İş kimliği CANLI kayıtlar arasında benzersizdir. Kısmi indeks bilinçlidir:
-- soft delete edilmiş bir kampanyanın kimliği yeniden kullanılabilmelidir,
-- aksi hâlde silinen bir kampanya adı sonsuza kadar rezerve kalırdı.
CREATE UNIQUE INDEX IF NOT EXISTS campaign_identifier_uniq
    ON campaign (campaign_identifier) WHERE deleted_at IS NULL;

-- promotion tek bir indirim tanımıdır.
--
-- code hem kupon kodu hem operatörün promosyonu andığı addır; canlı kayıtlar
-- arasında benzersizdir ve daima BÜYÜK harf saklanır (kupon kodları
-- büyük/küçük harf ayrımı yapmamalıdır — "yaz20" ile "YAZ20" aynı kupondur).
--
-- campaign_id ON DELETE SET NULL'dur: bir kampanya silindiğinde promosyonlar
-- SİLİNMEZ, yalnızca kapsız kalır. Silme, promosyon geçmişini yok etmemelidir.
CREATE TABLE IF NOT EXISTS promotion (
    id           TEXT PRIMARY KEY,
    code         TEXT        NOT NULL,
    is_automatic BOOLEAN     NOT NULL DEFAULT FALSE,
    type         TEXT        NOT NULL,
    campaign_id  TEXT        REFERENCES campaign(id) ON DELETE SET NULL,
    status       TEXT        NOT NULL,
    usage_limit  BIGINT,
    usage_count  BIGINT      NOT NULL DEFAULT 0,
    metadata     JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at   TIMESTAMPTZ,
    CONSTRAINT promotion_code_check        CHECK (code <> '' AND code = upper(code)),
    CONSTRAINT promotion_type_check        CHECK (type IN ('standard', 'buyget')),
    CONSTRAINT promotion_status_check      CHECK (status IN ('draft', 'active', 'inactive')),
    CONSTRAINT promotion_usage_limit_check CHECK (usage_limit IS NULL OR usage_limit >= 0),
    CONSTRAINT promotion_usage_count_check CHECK (usage_count >= 0),
    CONSTRAINT promotion_metadata_check    CHECK (jsonb_typeof(metadata) = 'object')
);

CREATE UNIQUE INDEX IF NOT EXISTS promotion_code_uniq
    ON promotion (code) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS promotion_campaign_idx
    ON promotion (campaign_id) WHERE deleted_at IS NULL;
-- Hesaplama her turda "aktif ve otomatik" promosyonları tarar; kısmi indeks o
-- taramayı tablo boyundan bağımsız kılar.
CREATE INDEX IF NOT EXISTS promotion_automatic_idx
    ON promotion (id) WHERE deleted_at IS NULL AND is_automatic AND status = 'active';

-- promotion_application_method indirimin NASIL uygulanacağıdır.
--
-- Bir promosyonun en fazla BİR yöntemi vardır (kısmi benzersiz indeks). İkiden
-- fazlası, aynı promosyonun iki farklı indirim ürettiği belirsiz bir duruma
-- yol açardı.
--
-- value alanı türe göre iki farklı şey ölçer: "fixed"te minor unit tutar,
-- "percentage"ta baz puan. Üst sınırlar bu yüzden türe bağlı kısıtlarla
-- ayrılmıştır; yüzde 10000 baz puanı (%100) aşamaz.
CREATE TABLE IF NOT EXISTS promotion_application_method (
    id            TEXT PRIMARY KEY,
    promotion_id  TEXT        NOT NULL REFERENCES promotion(id) ON DELETE CASCADE,
    type          TEXT        NOT NULL,
    target_type   TEXT        NOT NULL,
    allocation    TEXT        NOT NULL,
    value         BIGINT      NOT NULL,
    max_quantity  BIGINT,
    currency_code TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at    TIMESTAMPTZ,
    CONSTRAINT promotion_application_method_type_check   CHECK (type IN ('fixed', 'percentage')),
    CONSTRAINT promotion_application_method_target_check CHECK (target_type IN ('items', 'shipping_methods', 'order')),
    CONSTRAINT promotion_application_method_alloc_check  CHECK (allocation IN ('each', 'across')),
    CONSTRAINT promotion_application_method_value_check  CHECK (value >= 0),
    -- CASE kullanılır, "A OR B" DEĞİL: gerekçe campaign_budget_currency_check
    -- yanındadır — NULL bir para birimi, kısıtı NULL'a düşürüp sessizce
    -- geçirirdi.
    CONSTRAINT promotion_application_method_currency_check CHECK (
        CASE type
            WHEN 'fixed' THEN currency_code IS NOT NULL AND currency_code ~ '^[A-Z]{3}$'
            ELSE currency_code IS NULL
        END
    ),
    CONSTRAINT promotion_application_method_fixed_check CHECK (
        type <> 'fixed' OR value <= 1000000000000
    ),
    CONSTRAINT promotion_application_method_pct_check CHECK (
        type <> 'percentage' OR value <= 10000
    ),
    CONSTRAINT promotion_application_method_maxqty_check CHECK (
        max_quantity IS NULL OR (max_quantity >= 1 AND max_quantity <= 1000000)
    ),
    -- Sipariş hedefi TEK bir toplamı kalemlere dağıtır; "each" orada anlamsızdır.
    CONSTRAINT promotion_application_method_order_alloc_check CHECK (
        target_type <> 'order' OR allocation = 'across'
    )
);

CREATE UNIQUE INDEX IF NOT EXISTS promotion_application_method_promotion_uniq
    ON promotion_application_method (promotion_id) WHERE deleted_at IS NULL;

-- promotion_rule bir promosyonun uygulanma koşuludur.
--
-- rule_type koşulun NEYE baktığını söyler: "context" sepet bağlamına (para
-- birimi, bölge, müşteri grubu), "target" ise kalemin özniteliklerine.
--
-- Sütun adı "values" DEĞİLDİR: VALUES PostgreSQL'de ayrılmış bir sözcüktür ve
-- tırnaklanmadan kullanılamazdı (pricing.price_rule ile aynı gerekçe).
CREATE TABLE IF NOT EXISTS promotion_rule (
    id           TEXT PRIMARY KEY,
    promotion_id TEXT        NOT NULL REFERENCES promotion(id) ON DELETE CASCADE,
    rule_type    TEXT        NOT NULL,
    attribute    TEXT        NOT NULL,
    operator     TEXT        NOT NULL,
    rule_values  TEXT[]      NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at   TIMESTAMPTZ,
    CONSTRAINT promotion_rule_type_check      CHECK (rule_type IN ('context', 'target')),
    CONSTRAINT promotion_rule_attribute_check CHECK (attribute <> ''),
    CONSTRAINT promotion_rule_operator_check  CHECK (operator IN ('eq', 'ne', 'in', 'nin', 'gt', 'gte', 'lt', 'lte')),
    CONSTRAINT promotion_rule_values_check    CHECK (array_length(rule_values, 1) >= 1)
);

CREATE INDEX IF NOT EXISTS promotion_rule_promotion_idx
    ON promotion_rule (promotion_id) WHERE deleted_at IS NULL;

-- promotion_redemption kullanım sayacının DEFTERİDİR.
--
-- Sayacın kendisi promotion.usage_count ve campaign.budget_used sütunlarıdır;
-- bu tablo onların hangi referans için ne kadar arttığını tutar ve iki şeyi
-- mümkün kılar: idempotent kullanım (aynı referans ikinci kez sayacı
-- artırmaz) ve kesin geri alma (budget_delta aynen düşülür, tahmin edilmez).
--
-- reference bir sipariş/sepet kimliğidir ama foreign key DEĞİLDİR: o kayıt
-- başka bir modüle aittir (Prensip 2.2).
-- currency_code'un VARSAYILANI YOKTUR ve boş bırakılamaz: defterdeki her tutar
-- hangi para biriminde olduğunu TAŞIMAK ZORUNDADIR (plan Bölüm 8). Birimsiz bir
-- satır, kampanya bütçesinin hangi para biriminde tüketildiğini söylemez ve
-- serbest bırakmada aynı belirsizlikle geri düşülürdü.
CREATE TABLE IF NOT EXISTS promotion_redemption (
    id            TEXT PRIMARY KEY,
    promotion_id  TEXT        NOT NULL REFERENCES promotion(id) ON DELETE CASCADE,
    campaign_id   TEXT        REFERENCES campaign(id) ON DELETE SET NULL,
    reference     TEXT        NOT NULL,
    amount        BIGINT      NOT NULL DEFAULT 0,
    currency_code TEXT        NOT NULL,
    budget_delta  BIGINT      NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    released_at   TIMESTAMPTZ,
    CONSTRAINT promotion_redemption_reference_check CHECK (reference <> ''),
    CONSTRAINT promotion_redemption_amount_check    CHECK (amount >= 0 AND amount <= 1000000000000),
    CONSTRAINT promotion_redemption_delta_check     CHECK (budget_delta >= 0),
    CONSTRAINT promotion_redemption_currency_check  CHECK (currency_code ~ '^[A-Z]{3}$')
);

-- Aynı referans için AYNI ANDA yalnızca bir GEÇERLİ kullanım olabilir.
-- Kısmi indeks bilinçlidir: serbest bırakılmış bir kullanımın referansı yeniden
-- kullanılabilir (bırak → yeniden kullan), ama iki geçerli kullanım sayacı iki
-- kez artıramaz. Eşzamanlı iki Redeem'den biri bu indekse çarpar.
CREATE UNIQUE INDEX IF NOT EXISTS promotion_redemption_active_uniq
    ON promotion_redemption (promotion_id, reference) WHERE released_at IS NULL;
CREATE INDEX IF NOT EXISTS promotion_redemption_promotion_idx
    ON promotion_redemption (promotion_id);
