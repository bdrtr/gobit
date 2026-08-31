-- file modülünün şeması (plan Bölüm 5.6 — FileProvider soyutlaması).
--
-- Sahiplik: bu tablo YALNIZCA file modülüne aittir. uploaded_by bir kullanıcı
-- ya da API anahtarı kimliğidir ama FOREIGN KEY DEĞİLDİR (Prensip 2.2 —
-- cross-module FK yasağı): kimliği auth modülü sahiplenir.
--
-- Zaman: tüm damgalar timestamptz (UTC).

-- file_uploads YÜKLENEN DOSYALARIN DEFTERİDİR: dosyanın depoda nerede
-- durduğunu, İÇERİĞİNDEN tespit edilmiş tipini, boyutunu, özetini ve kimin
-- yüklediğini tutar.
--
-- # storage_key İSTEMCİDEN GELMEZ
--
-- Anahtarı sağlayıcı ÜRETİR (kimlik + tespit edilen tipten türeyen uzantı).
-- İstemcinin bildirdiği dosya adı ayrı bir sütunda (original_name) ve yalnızca
-- GÖSTERİM için durur; hiçbir yol ifadesine girmez. Yol geçişi ("../" ve her
-- kodlaması) böylece "temizlenerek" değil, YAPISAL olarak imkânsız kılınır:
-- temizlemek, her yeni kodlama numarasında kararı yeniden vermek demekti.
--
-- # storage_key BENZERSİZDİR
--
-- İki kayıt aynı dosyayı gösteremez. Kısıt iki şeyi birden korur: silme
-- (kaydı kaldırıp dosyayı silen akış) başka bir kaydın dosyasını götüremez ve
-- SUNUM yolu anahtardan kayda tek bir satırla ulaşabilir — sunulan
-- Content-Type o satırdan yazılır, dolayısıyla "hangi satır" sorusunun tek bir
-- cevabı olmalıdır.
--
-- # content_type İÇERİKTEN gelir
--
-- Sütun, istemcinin Content-Type başlığını DEĞİL net/http.DetectContentType'ın
-- ilk 512 bayttan tespit ettiği tipi taşır. İstemcinin bildirdiği tip bir
-- İDDİADIR: "image/png" diye gönderilen bir HTML dosyası, ona güvenen bir izin
-- listesinden geçer ve sunulduğunda tarayıcıda çalışır.
--
-- İzin listesi CHECK kısıtı olarak YAZILMAZ: kabul edilen tipler
-- yapılandırmadan gelir (FILE_ALLOWED_TYPES) ve kuruluma göre değişir; listeyi
-- şemaya sabitlemek, her ayar değişikliği için migration yazmayı zorunlu
-- kılardı. Deftere yazılan şey DENETLENMİŞ bir yüklemedir — denetimi yapan
-- servis katmanıdır.
--
-- # YUMUŞAK SİLME YOKTUR
--
-- Diğer modüllerin aksine deleted_at sütunu yoktur ve gerekçesi silmenin
-- kendisindedir: bir yükleme silindiğinde DOSYA da depodan silinir. Yumuşak
-- silinmiş bir satır, dosyası çoktan gitmiş bir kaydı listede tutar ("var ama
-- açılmıyor") ve benzersiz anahtarı İŞGAL etmeye devam ederdi.
CREATE TABLE IF NOT EXISTS file_uploads (
    id            TEXT        PRIMARY KEY,
    -- storage_key sağlayıcının ürettiği depo anahtarıdır; istemciden GELMEZ.
    storage_key   TEXT        NOT NULL,
    -- provider_id dosyayı yazan sağlayıcının kimliğidir. Saklanır çünkü
    -- kurulum sağlayıcı değiştirebilir ve eski kayıtları ancak onları yazan
    -- sağlayıcı okuyabilir.
    provider_id   TEXT        NOT NULL,
    -- content_type İÇERİKTEN tespit edilmiş tiptir; sunumda Content-Type
    -- başlığı bu sütundan yazılır.
    content_type  TEXT        NOT NULL,
    size          BIGINT      NOT NULL,
    -- checksum içeriğin SHA-256 özetidir (küçük harf onaltılık); teşhis için.
    checksum      TEXT        NOT NULL,
    -- original_name istemcinin bildirdiği addır ve YALNIZCA gösterim içindir.
    -- Boş olabilir: bazı istemciler ad göndermez ve bu bir hata değildir.
    original_name TEXT        NOT NULL DEFAULT '',
    -- url dosyanın erişilebilir adresidir; yerel sağlayıcıda köke görelidir.
    url           TEXT        NOT NULL,
    -- uploaded_by yükleyen çağıranın kimliğidir. FK YOKTUR (Prensip 2.2).
    uploaded_by   TEXT        NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT file_uploads_storage_key_not_empty  CHECK (storage_key <> ''),
    CONSTRAINT file_uploads_provider_id_not_empty  CHECK (provider_id <> ''),
    CONSTRAINT file_uploads_content_type_not_empty CHECK (content_type <> ''),
    CONSTRAINT file_uploads_url_not_empty          CHECK (url <> ''),
    -- Sıfır baytlık bir yükleme her zaman bir arızadır: içerikten tip tespiti
    -- de yapılamaz, sunulacak bir şey de yoktur.
    CONSTRAINT file_uploads_size_positive          CHECK (size > 0)
);

-- İki kayıt aynı depo anahtarını gösteremez; sunum yolunun anahtardan kayda
-- tek satırla ulaşmasını sağlayan kısıt budur.
CREATE UNIQUE INDEX IF NOT EXISTS file_uploads_storage_key_uniq
    ON file_uploads (storage_key);

-- Yönetim listesi en yeniden eskiye sayfalanır; indeks o sıralamayı karşılar.
CREATE INDEX IF NOT EXISTS file_uploads_recent_idx
    ON file_uploads (created_at DESC, id DESC);
