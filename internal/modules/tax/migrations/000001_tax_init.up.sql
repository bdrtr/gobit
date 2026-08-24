-- tax modülünün şeması (plan Faz 7, Bölüm 6).
--
-- Sahiplik: buradaki üç tablo YALNIZCA tax modülüne aittir. Modül İÇİ foreign
-- key'ler serbesttir ve kullanılır (oran bölgeye, kural orana bağlıdır); başka
-- bir modülün tablosuna REFERENCES VERİLMEZ (Prensip 2.2 — cross-module FK
-- yasağı). Bu yüzden tax_rate_rule.reference_id serbest METİNDİR: bir ürünün,
-- ürün tipinin ya da kargo seçeneğinin kimliğidir ve o kayıtların varlığı
-- BURADA doğrulanmaz.
--
-- Oran: rate_bps BAZ PUANDIR (2000 = %20) ve TAM SAYIDIR. Plan Bölüm 8 para ve
-- türevlerinde float yasaklar; %20'nin float karşılığı (0.2) bir tutarla
-- çarpıldığında kuruş düzeyinde sessiz yuvarlama üretirdi. Sütun adının
-- sonundaki birim bilinçlidir — "rate": 20 değerinin %20 mi 0,2 mi olduğu
-- belirsiz kalırdı.
--
-- Zaman: tüm damgalar TIMESTAMPTZ (UTC). Silme YUMUŞAKTIR (deleted_at) ve tüm
-- okuma sorguları deleted_at IS NULL süzer.

-- tax_region bir vergi bölgesidir: ülke kökü ya da o kökün altındaki eyalet.
--
-- # Hiyerarşi neden iki seviye
--
-- Vergi coğrafyası pratikte iki seviyedir: ülke (KDV/VAT) ve ülke altı birim
-- (ABD eyaleti, Kanada eyaleti, TR ili). parent_id bu bağı KENDİ tablosuna
-- verir; daha derin bir ağaç modellenebilir ama hesap yolu bilinçli olarak iki
-- seviyeyi çözer (bkz. service/calculate.go). Derinliği sınırlamak,
-- hesaplamanın özyinelemeli ve maliyeti öngörülemez bir sorguya dönüşmesini
-- engeller.
--
-- # "Bir ülkeye en fazla bir kök bölge" kuralı
--
-- Kural tax_region_country_root_uniq kısmi benzersiz indeksiyle VERİTABANINDA
-- zorlanır. Servis aynı denetimi daha okunabilir bir hatayla önce yapar, ama
-- son savunma burasıdır: iki eşzamanlı istek servisin "önce oku, sonra yaz"
-- denetimini birlikte geçebilir ve ülkeye iki kök bölge yazabilirdi. O andan
-- sonra hangi oranın uygulanacağı satır sırasına kalırdı.
--
-- # Eyalet ile kökün ülkesi neden ayrışamaz
--
-- parent_id TEK BAŞINA değil, (parent_id, country_code) İKİLİSİYLE üst satıra
-- bağlanır; hedefi de (id, country_code) benzersizliğidir. Sonuç: bir eyalet
-- satırının ülkesi, ebeveyninin ülkesinden FARKLI OLAMAZ. Tek sütunluk bir FK
-- bunu serbest bırakır ve "TR kökünün altında bir DE eyaleti" gibi bir kayıt
-- sessizce oluşabilirdi; hesap o eyaleti Almanya'da arar, hiç bulamazdı.
--
-- # provider_id neden DEVRALINIR
--
-- Boş provider_id "yerel" değil "ebeveynimin sağlayıcısı" demektir: hesap
-- zincirde en özelden genele yürür ve ilk DOLU değeri kullanır, hiçbiri dolu
-- değilse yerel hesaplamaya düşer (bkz. service.Service.providerFor). Kural bir
-- para hatasını kapatır — ülkesi dış bir otoriteye bağlıyken tek bir istisna
-- için açılan eyalet satırı, alanı boş kaldığı için o eyaletteki HER sepeti
-- sessizce yerel tablodan vergilerdi. Dolu bir değer o kimlikli dış sağlayıcıyı
-- çağırır; sağlayıcı kayıtlı değilse hesap SESSİZCE yerele düşmez, hata döner.
--
-- tax_region_provider_id_check alanı KIRPILMIŞ ve SINIRLI tutar. İki gerekçe:
-- kayıt araması kimliği kırparak yaptığı için kırpılmamış bir değer "saklanan"
-- ile "uygulanan" arasında ayrışma üretirdi; ve sınırsız bir metin alanı, tek
-- istekle tabloya megabaytlarca veri yazmanın en ucuz yoludur. Servis aynı
-- kuralı okunabilir bir hatayla önce uygular, bu kısıt doğrudan SQL'i de kapsar.
CREATE TABLE IF NOT EXISTS tax_region (
    id            TEXT PRIMARY KEY,
    -- country_code ISO 3166-1 alpha-2 kodudur; daima BÜYÜK harf saklanır.
    country_code  TEXT        NOT NULL,
    -- province_code ülke altı birimin kodudur; kök bölgede NULL'dur.
    province_code TEXT,
    -- parent_id kök bölgedir; kök satırda NULL'dur.
    parent_id     TEXT,
    -- provider_id vergi sağlayıcısının kimliğidir; boş ise ebeveynin
    -- sağlayıcısı devralınır, kök satırda yerel hesaplama uygulanır.
    provider_id   TEXT        NOT NULL DEFAULT '',
    metadata      JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at    TIMESTAMPTZ,

    CONSTRAINT tax_region_country_code_check
        CHECK (country_code ~ '^[A-Z]{2}$'),
    CONSTRAINT tax_region_province_code_check
        CHECK (province_code IS NULL OR province_code ~ '^[A-Z0-9][A-Z0-9-]{0,9}$'),
    CONSTRAINT tax_region_provider_id_check
        CHECK (provider_id = btrim(provider_id, E' \t\n\r\v\f') AND length(provider_id) <= 255),
    -- Kök bölgenin eyaleti, eyalet bölgesinin de kökü OLMAK ZORUNDADIR; ikisi
    -- birlikte doğar ya da hiç doğmaz. Aksi hâlde "ebeveyni olmayan eyalet"
    -- (hiç bulunamayan bir kayıt) ya da "eyalet kodu taşıyan kök" (ülkenin
    -- tamamı yerine tek bir ile uygulanan oran) mümkün olurdu.
    CONSTRAINT tax_region_hierarchy_check
        CHECK ((parent_id IS NULL AND province_code IS NULL)
            OR (parent_id IS NOT NULL AND province_code IS NOT NULL)),
    CONSTRAINT tax_region_self_parent_check
        CHECK (parent_id IS NULL OR parent_id <> id),
    -- Bileşik FK'nin hedefi; id zaten birincil anahtardır, bu kısıt yalnızca
    -- (parent_id, country_code) referansının bağlanabileceği bir birleşim
    -- sağlar.
    CONSTRAINT tax_region_id_country_uniq UNIQUE (id, country_code),
    CONSTRAINT tax_region_parent_fk
        FOREIGN KEY (parent_id, country_code) REFERENCES tax_region (id, country_code)
);

-- Bir ülkenin EN FAZLA bir kök vergi bölgesi olur.
CREATE UNIQUE INDEX IF NOT EXISTS tax_region_country_root_uniq
    ON tax_region (country_code)
    WHERE parent_id IS NULL AND deleted_at IS NULL;

-- Bir kökün altında aynı eyalet kodu iki kez bulunamaz. İndeks aynı zamanda
-- (ülke, eyalet) çözümünün okuma yoludur.
CREATE UNIQUE INDEX IF NOT EXISTS tax_region_province_uniq
    ON tax_region (parent_id, province_code)
    WHERE parent_id IS NOT NULL AND deleted_at IS NULL;

-- tax_rate bir vergi bölgesindeki orandır.
--
-- is_default bölgenin VARSAYILAN oranıdır: hiçbir kuralla eşleşmeyen her kalem
-- ona düşer. Bir bölgede en fazla bir varsayılan oran olabilir ve kural
-- tax_rate_default_uniq kısmi benzersiz indeksiyle zorlanır — ikinci bir
-- varsayılan, hangi oranın uygulanacağını satır sırasına bırakırdı.
--
-- code dış sistemlerle mutabakat içindir (örn. "KDV20") ve NULL olabilir. NULL
-- olabilmesi bilinçlidir: boş dize ile "kod yok" ayrımı, benzersizlik indeksinde
-- iki boş kodun çakışması demek olurdu. Kod verilmişse bölge içinde tekildir.
CREATE TABLE IF NOT EXISTS tax_rate (
    id            TEXT PRIMARY KEY,
    tax_region_id TEXT        NOT NULL,
    name          TEXT        NOT NULL,
    code          TEXT,
    -- rate_bps BAZ PUAN cinsinden orandır: 2000 = %20, 10000 = %100.
    rate_bps      INTEGER     NOT NULL DEFAULT 0,
    is_default    BOOLEAN     NOT NULL DEFAULT FALSE,
    metadata      JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at    TIMESTAMPTZ,

    CONSTRAINT tax_rate_name_check CHECK (name <> ''),
    CONSTRAINT tax_rate_code_check CHECK (code IS NULL OR code <> ''),
    CONSTRAINT tax_rate_bps_check  CHECK (rate_bps >= 0 AND rate_bps <= 10000),
    CONSTRAINT tax_rate_region_fk
        FOREIGN KEY (tax_region_id) REFERENCES tax_region (id)
);

CREATE UNIQUE INDEX IF NOT EXISTS tax_rate_default_uniq
    ON tax_rate (tax_region_id)
    WHERE is_default AND deleted_at IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS tax_rate_code_uniq
    ON tax_rate (tax_region_id, code)
    WHERE code IS NOT NULL AND deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS tax_rate_region_idx
    ON tax_rate (tax_region_id)
    WHERE deleted_at IS NULL;

-- tax_rate_rule bir oranın HANGİ kaleme uygulanacağını söyler.
--
-- reference kalemin türünü, reference_id o türdeki kimliği taşır. Kimlik başka
-- modüllere (product, fulfillment) aittir ve FK DEĞİLDİR (Prensip 2.2): tax o
-- kayıtları tanımaz, yalnızca kimlik eşitliğine bakar. Silinmiş bir ürünün
-- kuralı bu yüzden geride kalabilir; zararsızdır, çünkü o kimlikle hesaba giren
-- bir kalem de artık gelmez.
--
-- Varsayılan oranın kuralı OLMAZ: "kuralsız oran her şeye uygulanır" ile
-- "kurallı oran yalnızca eşleşene uygulanır" aynı satırda birleşseydi, oranın
-- kapsamı okunamaz hâle gelirdi. Kural veritabanında değil serviste zorlanır
-- (iki tabloya birden bakan bir CHECK yazılamaz); bkz. service/rule.go.
CREATE TABLE IF NOT EXISTS tax_rate_rule (
    id           TEXT PRIMARY KEY,
    tax_rate_id  TEXT        NOT NULL,
    reference    TEXT        NOT NULL,
    reference_id TEXT        NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at   TIMESTAMPTZ,

    CONSTRAINT tax_rate_rule_reference_check
        CHECK (reference IN ('product', 'product_type', 'shipping_option')),
    CONSTRAINT tax_rate_rule_reference_id_check CHECK (reference_id <> ''),
    CONSTRAINT tax_rate_rule_rate_fk
        FOREIGN KEY (tax_rate_id) REFERENCES tax_rate (id)
);

CREATE UNIQUE INDEX IF NOT EXISTS tax_rate_rule_uniq
    ON tax_rate_rule (tax_rate_id, reference, reference_id)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS tax_rate_rule_rate_idx
    ON tax_rate_rule (tax_rate_id)
    WHERE deleted_at IS NULL;
