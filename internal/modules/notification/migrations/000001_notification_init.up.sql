-- notification modülünün şeması (plan Bölüm 5.6).
--
-- Sahiplik: bu tablo YALNIZCA notification modülüne aittir. reference bir
-- sipariş kimliğidir ama FOREIGN KEY DEĞİLDİR (Prensip 2.2 — cross-module FK
-- yasağı): siparişi order modülü sahiplenir ve bağ, gerektiğinde Module Links
-- üzerinden kurulur.
--
-- Zaman: tüm damgalar timestamptz (UTC).

-- notification_deliveries TESLİM GÜNLÜĞÜDÜR: hangi şablonun, hangi kanaldan,
-- hangi referans için, hangi sağlayıcıyla gönderilmeye çalışıldığını ve
-- sonucunu tutar.
--
-- # ALICI ADRESİ SAKLANMAZ
--
-- Tabloda ne e-posta ne telefon vardır ve bu bilinçlidir. Adres zaten sipariş
-- kaydında durur; ikinci bir kopya, KVKK/GDPR silme talebinde temizlenmesi
-- gereken yerlerin sayısını artırır ve o kopyanın unutulması, silinmiş sanılan
-- bir kişinin verisinin sistemde kalması demektir. Günlüğün cevaplaması
-- gereken soru "kime gitti" değil, "gitti mi"dir; referans, kaydı siparişe
-- bağlamaya yeter.
--
-- # (template, reference) BENZERSİZDİR
--
-- İdempotency'nin tek dayanağı budur. Olay veri yolu bugün yeniden teslim
-- yapmaz (bkz. core/eventbus), ama bir olayın elle yeniden yayımlanması ya da
-- bir aboneliğin iki kez kurulması mümkündür; benzersizlik o kapıyı kapatır.
-- Kayıt gönderimden ÖNCE yazılır (status = 'pending'), yani eşzamanlı iki
-- işleyiciden yalnızca biri sağlayıcıya gider.
--
-- # YUMUŞAK SİLME YOKTUR
--
-- Diğer modüllerin aksine deleted_at sütunu yoktur. İki sebebi var: kayıt
-- kişisel veri taşımaz, yani silinmesi gereken bir şey içermez; ve yumuşak
-- silinmiş bir satır benzersiz anahtarı İŞGAL ETMEYE devam ederdi — silinmiş
-- görünen bir günlük kaydı, aynı bildirimin bir daha hiç gönderilememesi
-- anlamına gelirdi.
CREATE TABLE IF NOT EXISTS notification_deliveries (
    id          TEXT        PRIMARY KEY,
    -- template gönderilen bildirimin şablonudur; olay adıyla aynı seçilir
    -- (örn. "order.placed").
    template    TEXT        NOT NULL,
    -- channel gönderim kanalıdır ("email" | "sms").
    --
    -- CHECK kısıtı BİLİNÇLİ OLARAK YOKTUR: kanal listesi çekirdekte
    -- (internal/core/provider) tanımlıdır ve eklentiler yeni kanal getirebilir.
    -- Değerleri buraya sabitlemek, çekirdeğe eklenen her kanal için migration
    -- yazmayı zorunlu kılardı; oysa günlük, DENENEN gönderimi kaydeder —
    -- kanalın desteklenip desteklenmediğine sağlayıcı karar verir.
    channel     TEXT        NOT NULL,
    -- reference bildirimin bağlı olduğu kaydın kimliğidir (sipariş).
    -- FK YOKTUR (Prensip 2.2).
    reference   TEXT        NOT NULL,
    -- provider_id gönderimi yapan sağlayıcının kimliğidir. Saklanır çünkü
    -- yapılandırma değişir: bir ay önce hangi sağlayıcının denendiği, ancak
    -- kayıtta yazılıysa bilinebilir.
    provider_id TEXT        NOT NULL,
    status      TEXT        NOT NULL DEFAULT 'pending',
    -- error yalnızca status = 'failed' iken doludur; teşhis içindir.
    error       TEXT        NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT notification_deliveries_template_not_empty  CHECK (template <> ''),
    CONSTRAINT notification_deliveries_reference_not_empty CHECK (reference <> ''),
    CONSTRAINT notification_deliveries_channel_not_empty   CHECK (channel <> ''),
    -- 'pending' kalmış bir satır, sağlayıcıya gidildikten sonra sonucun
    -- yazılamadığı anlamına gelir; durum listesinde yer alması bu yüzden
    -- gereklidir — o satır bir arızanın kanıtıdır ve görünür kalmalıdır.
    CONSTRAINT notification_deliveries_status_valid
        CHECK (status IN ('pending', 'sent', 'failed', 'skipped'))
);

-- Aynı (şablon, referans) için İKİNCİ bir kayıt açılamaz; mükerrer bildirimi
-- nihai olarak durduran kısıt budur.
CREATE UNIQUE INDEX IF NOT EXISTS notification_deliveries_template_reference_uniq
    ON notification_deliveries (template, reference);

-- Yönetim listesi en yeniden eskiye sayfalanır; indeks o sıralamayı karşılar.
CREATE INDEX IF NOT EXISTS notification_deliveries_recent_idx
    ON notification_deliveries (created_at DESC, id DESC);

-- Bir siparişin bildirimlerini aramak günlüğün en sık sorusudur
-- ("müşteriye onay gitti mi").
CREATE INDEX IF NOT EXISTS notification_deliveries_reference_idx
    ON notification_deliveries (reference);
