-- auth_identity sorguları. Tüm okumalar deleted_at IS NULL filtresi uygular.
--
-- DİKKAT: password_hash bu sorguların dönüş kümesindedir ve repository
-- katmanının dışına ÇIKMAZ; servis onu yalnızca bcrypt karşılaştırmasına
-- verir, hiçbir log satırına ya da hata mesajına koymaz.
--
-- # updated_at bu tabloda bir GÜVENLİK ÇAPASIDIR
--
-- Sütun burada "satır en son ne zaman yazıldı" demek DEĞİLDİR: yalnızca hesap
-- sahibinin BİLEREK yaptığı iki işte ilerler — parola değişimi
-- (UpdatePasswordHash) ve çıkış (RevokeSessions). Oturum iptali ona dayanır:
-- servis, bu andan önce üretilmiş oturum jetonlarını reddeder
-- (bkz. service/session.go, sessionAnchor).
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

-- RevokeSessions oturum çapasını ilerletir; KİMLİK BİLGİSİNE DOKUNMAZ.
--
-- Çıkışın tamamı bu tek yazmadır. Jeton durum tutmaz ve jti bazlı bir kara
-- liste yoktur, dolayısıyla "şu jetonu düşür" diye bir işlem YAPILAMAZ;
-- yapılabilen tek şey çapayı ileri almak, yani ondan önce üretilmiş bütün
-- jetonları birden geçersizleştirmektir (bkz. service/session.go).
--
-- # Sağlayıcı SEÇİLMEZ: kullanıcının BÜTÜN kimlikleri ilerletilir
--
-- Filtre yalnızca user_id'dir. Bu tablo sağlayıcı BAŞINA satır tutar
-- ((user_id, provider) benzersizliği) ve tek bir sağlayıcı seçilseydi, ileride
-- OAuth eklendiği gün çıkış o sağlayıcıdan alınmış jetonları düşürmez, üstelik
-- bunu SESSİZCE yapardı: uç 200 döner, "çıkış yaptım" diyen kullanıcı hâlâ
-- oturumda kalırdı. Bugün tek sağlayıcı olduğu için etkilenen satır sayısı
-- birdir ve gözlemlenebilir davranış aynıdır; değişen şey, ikinci sağlayıcının
-- eklendiği günün sessiz açık BIRAKMAMASIDIR.
--
-- Okuma tarafı da aynı kuralı uygular: jeton doğrulanırken çapa tek bir
-- sağlayıcıdan değil, kullanıcının EN YENİ kimliğinden okunur
-- (bkz. GetSessionAnchor). İkisi ayrışsaydı buradaki yazma boşa giderdi.
--
-- password_hash'e DOKUNULMAZ: çıkış yapmak parolayı değiştirmez ve değişse
-- kullanıcı bir daha giremezdi.
--
-- failed_attempts ve locked_until'e de DOKUNULMAZ. Sıfırlansalardı çıkış ucu
-- kilidi temizlemenin yolu olurdu: kilitli hesabın elinde hâlâ geçerli bir
-- jeton varsa (kilit jetonu düşürmez) art arda "çıkış yap + yeniden dene" ile
-- sayaç sonsuza dek sıfırlanabilir, yani kilit hiç devreye girmezdi.
--
-- Kullanıcının hiç canlı kimliği yoksa HİÇBİR satır dönmez ve çağıran bunu
-- errors.NotFound'a çevirir; sessizce başarılı dönmek, hiçbir şey düşürmeyen
-- bir çıkışı başarı gibi göstermek olurdu.
-- name: RevokeSessions :many
UPDATE auth_identity SET
    updated_at = $2
WHERE user_id = $1 AND deleted_at IS NULL
RETURNING *;

-- GetSessionAnchor kullanıcının EN YENİ oturum çapasını döner.
--
-- Jeton doğrulaması bu tek değere dayanır: "iat" bundan önceyse jeton
-- reddedilir (bkz. service/interop.go, principalFromToken).
--
-- # Neden EN YENİ (ve neden tek bir sağlayıcı değil)
--
-- Jetonun hangi sağlayıcıdan alındığını söyleyen bir iddia YOKTUR; iddia
-- olmadığı için çapa sağlayıcıya göre seçilemez. Seçilebilen iki uç vardır ve
-- en ESKİSİNİ almak yanlış olurdu: çapası hiç ilerlemeyen tek bir satır (örn.
-- parola değişimi yalnızca emailpass satırını yazar) iptalin tamamını etkisiz
-- bırakırdı. EN YENİ olan alınır — belirsizlik güvenlik lehine çözülür, bedeli
-- bir sağlayıcıdaki iptalin ötekinin jetonlarını da düşürmesidir.
--
-- Kullanıcının sağlayıcı sayısı elle ölçülür; sıralama, user_id önekiyle
-- taranan indeksten (auth_identity_user_provider_uniq) gelen bir avuç satır
-- üzerindedir.
--
-- Canlı kimlik hiç yoksa satır dönmez: çağıran bunu errors.NotFound'a çevirir
-- ve jetonu reddeder, çünkü jetonun ne zaman geçersizleştiğini söyleyecek bir
-- değer kalmamıştır.
-- name: GetSessionAnchor :one
SELECT updated_at FROM auth_identity
WHERE user_id = $1 AND deleted_at IS NULL
ORDER BY updated_at DESC
LIMIT 1;

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
