-- auth modülünün şeması (plan Faz 8, Bölüm 6).
--
-- Tablolar YALNIZCA bu modüle aittir. Prensip 2.2 gereği başka bir modülün
-- tablosuna REFERENCES verilmez; modülün KENDİ tabloları arasındaki foreign
-- key'ler serbesttir ve kullanılır.
--
-- Zaman sütunları TIMESTAMPTZ'dir ve daima UTC yazılır; silme SOFT'tur
-- (deleted_at) ve tüm okuma sorguları deleted_at IS NULL filtresi uygular.
--
-- GÜVENLİK NOTU: bu şemada iki sütun sır taşır ve ikisi de HASH saklar,
-- düz metin ASLA yazılmaz: auth_identity.password_hash (bcrypt) ve
-- api_key.token_hash (SHA-256). Gerekçeleri ilgili tabloların üstündedir.

-- auth_user bir YÖNETİM kullanıcısıdır (admin paneline giren kişi).
--
-- Tablo adı "user" DEĞİLDİR: PostgreSQL'de user ayrılmış bir anahtar kelimedir
-- ve her sorguda tırnaklanmak zorunda kalırdı.
--
-- Müşteriyle karıştırılmamalıdır: mağazadan alışveriş yapan kişi customer
-- modülünün verisidir, buradaki kayıt yönetim yüzeyine erişen personeldir.
-- İki kavramın ayrı modüllerde durması bilinçlidir; bir müşterinin admin
-- yetkisi kazanması diye bir yol yoktur.
--
-- PAROLA BURADA DEĞİLDİR: kimlik doğrulama yöntemi auth_identity tablosunda
-- tutulur (gerekçe orada).
CREATE TABLE IF NOT EXISTS auth_user (
    id         TEXT PRIMARY KEY,
    email      TEXT        NOT NULL,
    first_name TEXT        NOT NULL DEFAULT '',
    last_name  TEXT        NOT NULL DEFAULT '',
    avatar_url TEXT        NOT NULL DEFAULT '',
    scopes     TEXT[]      NOT NULL DEFAULT ARRAY['admin']::text[],
    metadata   JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ,
    CONSTRAINT auth_user_email_check       CHECK (email <> '' AND email = lower(email)),
    CONSTRAINT auth_user_email_shape_check CHECK (email ~ '^[^[:space:]@]+@[^[:space:]@]+\.[^[:space:]@]+$'),
    CONSTRAINT auth_user_email_len_check   CHECK (length(email) <= 320),
    CONSTRAINT auth_user_scopes_check      CHECK (array_position(scopes, '') IS NULL)
);

-- E-posta CANLI kullanıcılar arasında benzersizdir.
--
-- Benzersizlik giriş akışının ön şartıdır: "e-posta + parola" ile giren bir
-- kullanıcı için iki eşleşen satır olsaydı, giriş hangi kimliği vereceğini
-- bilemezdi. deleted_at IS NULL koşulu silinen bir kullanıcının e-postasını
-- yeniden kullanılabilir bırakır.
CREATE UNIQUE INDEX IF NOT EXISTS auth_user_email_uniq
    ON auth_user (email)
    WHERE deleted_at IS NULL;

-- auth_identity bir kullanıcının TEK bir kimlik doğrulama yöntemidir.
--
-- Neden auth_user'dan ayrı: bir kullanıcının birden çok giriş yolu olabilir.
-- Bugün yalnızca "emailpass" (e-posta + parola) vardır; yarın OAuth eklendiğinde
-- AYNI kullanıcıya ikinci bir satır bağlanır ve kullanıcı kaydına dokunulmaz.
-- Parola sütunu auth_user'da olsaydı, parolasız (yalnızca OAuth) bir kullanıcı
-- ifade edilemez ya da boş parola ile temsil edilirdi.
--
-- password_hash BCRYPT çıktısıdır; düz parola ASLA yazılmaz, ASLA loglanmaz.
-- bcrypt maliyet parametresi hash'in İÇİNDE saklanır: maliyet ileride
-- artırıldığında eski hash'ler kendi maliyetleriyle doğrulanmaya devam eder.
-- Parolası olmayan (örn. yalnızca OAuth) kimlikte sütun boştur ve giriş
-- REDDEDİLİR.
CREATE TABLE IF NOT EXISTS auth_identity (
    id                TEXT PRIMARY KEY,
    user_id           TEXT        NOT NULL REFERENCES auth_user(id) ON DELETE CASCADE,
    provider          TEXT        NOT NULL,
    provider_identity TEXT        NOT NULL,
    password_hash     TEXT        NOT NULL DEFAULT '',
    failed_attempts   INTEGER     NOT NULL DEFAULT 0,
    locked_until      TIMESTAMPTZ,
    last_login_at     TIMESTAMPTZ,
    metadata          JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at        TIMESTAMPTZ,
    CONSTRAINT auth_identity_provider_check  CHECK (provider <> ''),
    CONSTRAINT auth_identity_identity_check  CHECK (provider_identity <> ''),
    CONSTRAINT auth_identity_attempts_check  CHECK (failed_attempts >= 0)
);

-- Bir sağlayıcıdaki bir kimlik EN FAZLA BİR kullanıcıya bağlanır.
--
-- Koşul olmasaydı aynı e-posta iki kullanıcıya "emailpass" kimliği olarak
-- bağlanabilir ve giriş iki farklı kişiyi eşleştirirdi.
CREATE UNIQUE INDEX IF NOT EXISTS auth_identity_provider_uniq
    ON auth_identity (provider, provider_identity)
    WHERE deleted_at IS NULL;

-- Bir kullanıcının bir sağlayıcıda EN FAZLA BİR kimliği olur.
--
-- Üstteki indeks TERS yönü kapatır (bir sağlayıcıdaki kimlik iki kullanıcıya
-- bağlanamaz) ama aynı kullanıcıya aynı sağlayıcıdan İKİNCİ bir satır
-- açılmasını engellemez. Kod bunun olmadığını varsayar: kimlik
-- (user_id, provider) ile TEK satır olarak okunur ve parola doğrulaması,
-- başarısız deneme sayacı ile kilit hep o satıra yazılır. İki satır olsaydı
-- hangisinin okunacağı sorgunun sırasına kalırdı: parola değişikliği yalnızca
-- birine yazılır, öteki eski parolayla açık kalır ve deneme sayacı ikiye
-- bölündüğü için kilit eşiği sessizce iki katına çıkardı.
--
-- Kısmi koşul (deleted_at IS NULL) silinen bir kimliğin yerine yenisinin
-- açılabilmesi içindir; şemadaki diğer benzersizlikler de aynı kuralı izler.
--
-- Ayrı bir user_id indeksi TUTULMAZ: bu indeks user_id ÖNEKİYLE de aranabilir,
-- ikincisi yalnızca her yazımda ödenen fazladan bir maliyet olurdu.
CREATE UNIQUE INDEX IF NOT EXISTS auth_identity_user_provider_uniq
    ON auth_identity (user_id, provider)
    WHERE deleted_at IS NULL;

-- sales_channel bir satış kanalıdır (örn. "Web", "Mobil uygulama", "Bayi").
--
-- Publishable API anahtarları kanallara bağlanır ve mağaza isteği hangi
-- kanaldan geldiğini bu bağdan öğrenir. Katalog süzmesi (hangi ürün hangi
-- kanalda görünür) product ↔ sales_channel linkiyle kurulur ve auth o linki
-- hiç görmez (Prensip 2.2).
CREATE TABLE IF NOT EXISTS sales_channel (
    id          TEXT PRIMARY KEY,
    name        TEXT        NOT NULL,
    description TEXT        NOT NULL DEFAULT '',
    is_disabled BOOLEAN     NOT NULL DEFAULT FALSE,
    metadata    JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at  TIMESTAMPTZ,
    CONSTRAINT sales_channel_name_check     CHECK (name <> ''),
    CONSTRAINT sales_channel_name_len_check CHECK (length(name) <= 255)
);

-- Kanal adı CANLI kanallar arasında benzersizdir; silinen adın yeniden
-- kullanılabilmesi için koşul deleted_at IS NULL'dır.
CREATE UNIQUE INDEX IF NOT EXISTS sales_channel_name_uniq
    ON sales_channel (name)
    WHERE deleted_at IS NULL;

-- api_key bir makine kimliğidir; iki TÜRÜ vardır ve ikisi aynı şey DEĞİLDİR.
--
--   secret      — yönetim yüzeyine erişen SIRDIR. Sunucuda saklanır,
--                 tarayıcıya asla verilmez, sızması admin erişimi demektir.
--   publishable — SIR DEĞİLDİR. Tarayıcıda görünür, tek işi isteği bir satış
--                 kanalına bağlamaktır; hiçbir yetki taşımaz.
--
-- Anahtarın KENDİSİ saklanmaz, yalnızca token_hash (SHA-256, hex) saklanır.
-- Neden bcrypt değil: anahtar bizim ürettiğimiz 256 bitlik rastgele bir
-- dizedir, sözlük saldırısına açık bir insan parolası değildir; yavaş hash'in
-- koruduğu şey (çevrimdışı kaba kuvvet) burada zaten imkânsızdır. Buna karşılık
-- bu hash HER İSTEKTE hesaplanır ve bcrypt her admin isteğine ~250 ms eklerdi.
-- Ayrıca bcrypt'in satır başına tuzu, anahtarı bulmak için TÜM tabloyu taramayı
-- gerektirirdi; SHA-256 tek ve indekslenebilir bir aramadır.
--
-- created_by anahtarı üretenin kimliğidir ve foreign key TAŞIMAZ: değer bir
-- kullanıcı kimliği ("user_…") olabileceği gibi başka bir gizli anahtarın
-- kimliği ("apikey_…") de olabilir, yani tek bir tabloya işaret etmez.
CREATE TABLE IF NOT EXISTS api_key (
    id           TEXT PRIMARY KEY,
    type         TEXT        NOT NULL,
    title        TEXT        NOT NULL,
    token_hash   TEXT        NOT NULL,
    redacted     TEXT        NOT NULL,
    scopes       TEXT[]      NOT NULL DEFAULT '{}'::text[],
    created_by   TEXT        NOT NULL DEFAULT '',
    last_used_at TIMESTAMPTZ,
    revoked_at   TIMESTAMPTZ,
    revoked_by   TEXT        NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at   TIMESTAMPTZ,
    CONSTRAINT api_key_type_check       CHECK (type IN ('publishable', 'secret')),
    CONSTRAINT api_key_title_check      CHECK (title <> ''),
    CONSTRAINT api_key_title_len_check  CHECK (length(title) <= 255),
    CONSTRAINT api_key_token_hash_check CHECK (token_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT api_key_scopes_check     CHECK (array_position(scopes, '') IS NULL)
);

-- token_hash benzersizdir ve bu indeks KISMİ DEĞİLDİR.
--
-- deleted_at ya da revoked_at koşulu eklenseydi, iptal edilmiş bir anahtarın
-- hash'i yeniden kullanılabilir hâle gelirdi. Anahtar 256 bit rastgele
-- olduğundan çakışma pratikte imkânsızdır; indeks bu yüzden bir kısıt değil,
-- üretecin bozulduğunu ANINDA gösteren bir alarmdır.
CREATE UNIQUE INDEX IF NOT EXISTS api_key_token_hash_uniq
    ON api_key (token_hash);

CREATE INDEX IF NOT EXISTS api_key_type_idx
    ON api_key (type)
    WHERE deleted_at IS NULL;

-- api_key_sales_channel publishable anahtar ile satış kanalı arasındaki
-- ÇOKA-ÇOK bağdır.
--
-- Bileşik birincil anahtar aynı bağın iki kez kurulmasını engeller; bağ
-- kümedir, çokluk taşımaz. İki tablo da bu modüle ait olduğu için foreign
-- key serbesttir (Prensip 2.2 yalnızca MODÜLLER ARASI FK'yi yasaklar).
CREATE TABLE IF NOT EXISTS api_key_sales_channel (
    api_key_id       TEXT        NOT NULL REFERENCES api_key(id) ON DELETE CASCADE,
    sales_channel_id TEXT        NOT NULL REFERENCES sales_channel(id) ON DELETE CASCADE,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (api_key_id, sales_channel_id)
);

-- Bir kanala bağlı anahtarları listelemek birincil anahtarın ÖNEKİNİ
-- kullanamaz; ters yön için ayrı indeks gerekir.
CREATE INDEX IF NOT EXISTS api_key_sales_channel_channel_idx
    ON api_key_sales_channel (sales_channel_id);
