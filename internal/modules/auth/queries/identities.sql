-- auth_identity sorguları. Tüm okumalar deleted_at IS NULL filtresi uygular.
--
-- DİKKAT: password_hash bu sorguların dönüş kümesindedir ve repository
-- katmanının dışına ÇIKMAZ; servis onu yalnızca bcrypt karşılaştırmasına
-- verir, hiçbir log satırına ya da hata mesajına koymaz.
--
-- # updated_at bu tabloda bir GÜVENLİK ÇAPASIDIR
--
-- Sütun burada "satır en son ne zaman yazıldı" demek DEĞİLDİR: yalnızca KİMLİK
-- BİLGİSİ (parola) değiştiğinde ilerler ve oturum iptali ona dayanır — servis,
-- bu andan önce üretilmiş oturum jetonlarını reddeder (bkz. service/password.go,
-- passwordChangedAt).
--
-- Bu yüzden giriş sayaçlarını yazan sorgular (RegisterLoginFailure,
-- RegisterLoginSuccess) updated_at'e DOKUNMAZ. Dokunsalardı:
--
--   * tek bir HATALI giriş denemesi yöneticinin bütün oturumlarını düşürürdü;
--     saldırganın yalnızca e-postayı bilmesi yeterdi ve elinde hedefli bir
--     hizmet dışı bırakma aracı olurdu,
--   * ikinci bir cihazdan giriş yapmak birinci cihazın oturumunu kapatırdı.
--
-- Aynı ayrım bu şemada zaten var: api_key.last_used_at de updated_at'i
-- kıpırdatmıyor (bkz. api_keys.sql, MarkAPIKeyUsed). Kullanım/deneme sayaçları
-- telemetridir, kaydın içeriği değildir.

-- name: InsertIdentity :one
INSERT INTO auth_identity (
    id, user_id, provider, provider_identity, password_hash, metadata, created_at, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $7)
RETURNING *;

-- name: GetIdentityOfUser :one
SELECT * FROM auth_identity
WHERE user_id = $1 AND provider = $2 AND deleted_at IS NULL;

-- UpdatePasswordHash parolayı değiştirir ve kilit sayaçlarını SIFIRLAR.
--
-- Sıfırlama şarttır: parolasını değiştiren kullanıcı, eski parolayla yapılmış
-- başarısız denemelerin bıraktığı kilitle karşılaşmamalıdır.
--
-- updated_at'in ilerlemesi de şarttır: MEVCUT oturum jetonlarını düşüren tek
-- şey odur (dosya başındaki "güvenlik çapası" notu). Parola değişip çapa
-- yerinde kalsaydı, sızmış bir jeton süresi dolana kadar geçerli kalırdı.
-- name: UpdatePasswordHash :one
UPDATE auth_identity SET
    password_hash   = $2,
    failed_attempts = 0,
    locked_until    = NULL,
    updated_at      = $3
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- RegisterLoginFailure başarısız bir giriş denemesini ATOMİK olarak sayar.
--
-- Sayaç neden SQL'de artırılıyor: sayı Go tarafında okunup geri yazılsaydı,
-- aynı anda gönderilen yüzlerce istek hepsi "0" okuyup hepsi "1" yazardı ve
-- kilit hiç devreye girmezdi. CTE'deki FOR UPDATE satırı işlem sonuna kadar
-- kilitler, böylece artırma sıralanır.
--
-- Süresi DOLMUŞ bir kilit yeni bir pencerenin başlangıcı sayılır ve sayaç 1'e
-- döner: kilidini bekleyip tekrar deneyen kullanıcı, tek bir yanlışta yeniden
-- kilitlenmemelidir.
--
-- AKTİF kilitte bu sorgu HİÇ çağrılmaz; servis isteği daha önce reddeder.
-- Bu yüzden "eşiğin altındaysa locked_until = NULL" dalı aktif bir kilidi
-- silemez.
--
-- updated_at'e DOKUNULMAZ: başarısız bir deneme, kurbanın açık oturumlarını
-- düşürmemelidir (dosya başındaki "güvenlik çapası" notu).
-- name: RegisterLoginFailure :one
WITH sonraki AS (
    SELECT
        k.id AS kimlik,
        CASE
            WHEN k.locked_until IS NOT NULL AND k.locked_until <= sqlc.arg('now')::timestamptz THEN 1
            ELSE k.failed_attempts + 1
        END AS deneme
    FROM auth_identity k
    WHERE k.id = sqlc.arg('id') AND k.deleted_at IS NULL
    FOR UPDATE
)
UPDATE auth_identity AS i SET
    failed_attempts = sonraki.deneme,
    locked_until    = CASE
        WHEN sonraki.deneme >= sqlc.arg('threshold')::int THEN sqlc.arg('locked_until')::timestamptz
        ELSE NULL::timestamptz
    END
FROM sonraki
WHERE i.id = sonraki.kimlik
RETURNING i.*;

-- RegisterLoginSuccess başarılı girişte sayaçları temizler.
--
-- updated_at'e DOKUNULMAZ: yeni bir giriş, kullanıcının başka cihazlardaki
-- oturumlarını kapatmamalıdır (dosya başındaki "güvenlik çapası" notu).
-- name: RegisterLoginSuccess :exec
UPDATE auth_identity SET
    failed_attempts = 0,
    locked_until    = NULL,
    last_login_at   = $2
WHERE id = $1 AND deleted_at IS NULL;
