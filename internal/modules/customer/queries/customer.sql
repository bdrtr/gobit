-- customer sorguları. Tüm okumalar deleted_at IS NULL filtresi uygular.
--
-- TEK istisna dosyanın sonundaki UNUTULMA (erasure) sorgularıdır ve istisna
-- bilinçlidir: yumuşak silinmiş bir satır kişisel verisini OLDUĞU GİBİ
-- taşımaya devam eder. Gerekçe LockCustomerForErasure başlığında yazılıdır.
-- (Köşeli parantezli godoc bağlantısı DEĞİL: sqlc bu başlığı ürettiği pakete
-- olduğu gibi kopyalar ve orada o ad bir metottur, paket düzeyinde bir
-- bildirim değil — bağlantı çözülmezdi.)

-- name: InsertCustomer :one
INSERT INTO customer (id, email, first_name, last_name, phone, has_account, metadata, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)
RETURNING *;

-- name: GetCustomer :one
SELECT * FROM customer
WHERE id = $1 AND deleted_at IS NULL;

-- GetCustomerForUpdate müşteriyi okur ve satırını İŞLEM SONUNA KADAR kilitler.
--
-- Aynı müşteriye yapılan durum değiştiren akışlar (misafirden hesaba geçiş,
-- varsayılan adres atama) bu kilidi HER ZAMAN İLK sırada alır. Sıra sabit
-- olduğu için iki akış birbirini ters sırada bekleyemez; kilitlenme (deadlock)
-- yapısal olarak imkânsızdır.
--
-- FOR UPDATE kilit alındıktan sonra WHERE koşulunu YENİDEN değerlendirir; araya
-- giren bir silme bu yüzden "kayıt yok" olarak görünür.
-- name: GetCustomerForUpdate :one
SELECT * FROM customer
WHERE id = $1 AND deleted_at IS NULL
FOR UPDATE;

-- GetAccountByEmail YALNIZCA kayıtlı hesabı arar.
--
-- Misafir kayıtları aynı e-postayı paylaşabildiği için "e-postaya göre tek
-- müşteri" ancak hesaplar için anlamlıdır; bu sorgunun has_account süzgeci
-- kısmi benzersiz indeksin kapsamıyla birebir aynıdır.
-- name: GetAccountByEmail :one
SELECT * FROM customer
WHERE email = $1 AND has_account AND deleted_at IS NULL;

-- AccountEmailTakenByOther verilen e-postayı BAŞKA bir hesabın kullanıp
-- kullanmadığını bildirir; misafirden hesaba geçişin ön denetimidir.
-- name: AccountEmailTakenByOther :one
SELECT EXISTS (
    SELECT 1 FROM customer
    WHERE email = $1 AND id <> $2 AND has_account AND deleted_at IS NULL
);

-- ListCustomers süzgeçlenmiş ve sayfalanmış müşteri listesini döner.
--
-- group_id süzgeci üyelik satırına DEĞİL, canlı gruba bakar: üyelik satırları
-- grup yumuşak silindiğinde de yerinde kalır ve yalnızca üyeliğe bakan bir
-- süzgeç, silinmiş bir grubun üyelerini listelemeye devam ederdi. Grubu okuyan
-- her sorgu deleted_at IS NULL süzer; burası da aynı kurala uyar.
-- name: ListCustomers :many
SELECT c.* FROM customer c
WHERE c.deleted_at IS NULL
  AND (sqlc.narg('email')::text IS NULL OR c.email = sqlc.narg('email')::text)
  AND (sqlc.narg('has_account')::boolean IS NULL OR c.has_account = sqlc.narg('has_account')::boolean)
  AND (sqlc.narg('group_id')::text IS NULL OR EXISTS (
        SELECT 1 FROM customer_group_customer m
        JOIN customer_group g ON g.id = m.customer_group_id AND g.deleted_at IS NULL
        WHERE m.customer_id = c.id AND m.customer_group_id = sqlc.narg('group_id')::text))
  AND (c.created_at, c.id) < (
    COALESCE(sqlc.narg('after_at')::timestamptz, 'infinity'::timestamptz),
    COALESCE(sqlc.narg('after_id')::text, '')
  )
ORDER BY c.created_at DESC, c.id DESC
LIMIT sqlc.arg('lim')::int OFFSET sqlc.arg('off')::int;

-- name: CountCustomers :one
SELECT count(*) FROM customer c
WHERE c.deleted_at IS NULL
  AND (sqlc.narg('email')::text IS NULL OR c.email = sqlc.narg('email')::text)
  AND (sqlc.narg('has_account')::boolean IS NULL OR c.has_account = sqlc.narg('has_account')::boolean)
  AND (sqlc.narg('group_id')::text IS NULL OR EXISTS (
        SELECT 1 FROM customer_group_customer m
        JOIN customer_group g ON g.id = m.customer_group_id AND g.deleted_at IS NULL
        WHERE m.customer_id = c.id AND m.customer_group_id = sqlc.narg('group_id')::text));

-- name: ListCustomersByIDs :many
SELECT * FROM customer
WHERE id = ANY(@ids::text[]) AND deleted_at IS NULL
ORDER BY id;

-- UpdateCustomer verilmeyen alanları OLDUĞU GİBİ bırakır.
--
-- COALESCE ile yazılan bu kısmi güncelleme, "alan gönderilmedi" ile "alan boşa
-- çekildi" ayrımını korur: NULL parametre eski değeri saklar, boş dize gerçek
-- bir temizlemedir.
-- name: UpdateCustomer :one
UPDATE customer SET
    email      = COALESCE(sqlc.narg('email')::text, email),
    first_name = COALESCE(sqlc.narg('first_name')::text, first_name),
    last_name  = COALESCE(sqlc.narg('last_name')::text, last_name),
    phone      = COALESCE(sqlc.narg('phone')::text, phone),
    metadata   = COALESCE(sqlc.narg('metadata')::jsonb, metadata),
    updated_at = sqlc.arg('updated_at')
WHERE id = sqlc.arg('id') AND deleted_at IS NULL
RETURNING *;

-- PromoteCustomerToAccount misafiri hesaba çevirir.
--
-- has_account = FALSE koşulu şarttır: zaten hesap olan bir kaydı yeniden
-- yükseltmek sessiz bir no-op olurdu ve çağıran işlemin gerçekleştiğini
-- sanırdı. Koşul tutmazsa satır dönmez ve servis durumu ayırt eder.
-- name: PromoteCustomerToAccount :one
UPDATE customer
SET has_account = TRUE, updated_at = $2
WHERE id = $1 AND deleted_at IS NULL AND has_account = FALSE
RETURNING *;

-- name: SoftDeleteCustomer :one
UPDATE customer
SET deleted_at = $2, updated_at = $2
WHERE id = $1 AND deleted_at IS NULL
RETURNING id;

-- SoftDeleteAddressesOfCustomer müşteri silinirken adreslerini de siler.
--
-- Foreign key ON DELETE CASCADE yalnızca GERÇEK silmede çalışır; yumuşak silme
-- bir UPDATE olduğu için adresleri kendiliğinden götürmez. Silinmiş bir
-- müşterinin canlı adresleri geride kalsaydı, adres listeleri sahipsiz kayıt
-- gösterirdi.
-- name: SoftDeleteAddressesOfCustomer :exec
UPDATE customer_address
SET deleted_at = $2, updated_at = $2
WHERE customer_id = $1 AND deleted_at IS NULL;

-- LockCustomerForErasure unutulma isteğinin ULAŞTIĞI satırı kimliğe göre okur
-- ve işlem sonuna kadar kilitler.
--
-- deleted_at SÜZGECİ YOKTUR ve bu, dosyanın geri kalanından ayrıldığı tek
-- noktadır. Sebep ölçülebilir: SoftDeleteCustomer yalnızca deleted_at ile
-- updated_at yazar, tek bir kişisel sütuna dokunmaz. Yumuşak silinmiş bir
-- müşterinin e-postası, adı ve telefonu bu yüzden tabloda AYNEN durur. Süzgeç
-- konsaydı, kaydı silinmiş bir kişiye "veriniz anonimleştirildi" denirken veri
-- yerinde kalırdı — ve rapor bunu hiçbir yerde söylemezdi.
--
-- Silme (bookkeeping) ile unutulma (hukuki cevap) iki AYRI iştir; bu sorgunun
-- silinmiş satırı da görmesi o ayrımın veritabanı tarafındaki karşılığıdır.
--
-- FOR UPDATE şarttır: kilit alınmadan okunan satırın üzerine yazılırken araya
-- giren bir güncelleme (örn. müşterinin vitrinde kendi adını değiştirmesi)
-- kaybolur ya da anonimleştirmeden SONRA yerine oturur; kilit, satırı okuyan
-- ile yazanın aynı işlem olmasını garanti eder.
--
-- Sorgu YALNIZCA kimliği döner. Eskiden e-postayı da dönerdi çünkü satırın
-- zaten anonim olup olmadığına ondan karar veriliyordu; o karar artık
-- AnonymizeCustomer'ın WHERE'inde, sütunların GERÇEK değerlerine bakılarak
-- veriliyor (gerekçesi orada yazılıdır).
-- name: LockCustomerForErasure :one
SELECT id FROM customer
WHERE id = $1
FOR UPDATE;

-- LockCustomersByEmailForErasure e-postaya göre ulaşılan TÜM satırları kilitler.
--
-- Çoğul olması zorunludur: aynı e-postayla istenildiği kadar MİSAFİR kaydı
-- açılabilir (bkz. customer_account_email_uniq) ve hepsi aynı kişidir. Yalnızca
-- hesabı arayan bir sorgu (GetAccountByEmail) o kişinin misafir kayıtlarını
-- olduğu gibi bırakırdı.
--
-- ORDER BY id yalnızca belirlilik için değildir: kilit sırasını SABİTLER.
-- Aynı e-postaya iki eşzamanlı unutulma isteği gelirse ikisi de satırları aynı
-- sırada kilitler ve karşılıklı bekleme (deadlock) yapısal olarak imkânsız olur.
-- name: LockCustomersByEmailForErasure :many
SELECT id FROM customer
WHERE email = $1
ORDER BY id
FOR UPDATE;

-- LockCustomersByIDOrEmailForErasure kimliği VE e-postayı BİRLİKTE taşıyan
-- özneyi tek sorguda çözer.
--
-- Böyle bir özne kural dışı değil, YÖNETİM UCUNUN normalidir (bkz.
-- internal/app/erasure.go: istek gövdesindeki iki alan da doğrudan
-- erasure.Subject'e geçer). Kaydı olan bir müşteri aynı adresle misafir olarak
-- da alışveriş yapmış olabilir; kimliği o kaydı, e-posta ötekileri gösterir ve
-- kişi hepsidir. "Kimlik varsa e-postaya bakma" kuralı tam da bu kişide
-- misafir satırlarını olduğu gibi bırakırdı.
--
-- İki ayrı sorgu yerine TEK bir OR'lu sorgu olmasının sebebi kilit sırasıdır.
-- Önce kimliği, sonra e-postayı kilitleyen bir sıra, e-postayla gelen ikinci
-- bir isteğin id sırasıyla ilerlemesiyle ters düşebilirdi: (cust_C, adres) ile
-- gelen istek C'yi tutup A'yı beklerken, yalnız adresle gelen istek A'yı tutup
-- C'yi bekler — karşılıklı bekleme. Tek sorgu satırların HEPSİNİ tek bir id
-- sırasında kilitler, o sıra e-posta sorgusununkiyle aynıdır ve kilitlenme
-- yapısal olarak imkânsız kalır. Aynı satır iki koşulu birden sağlasa bile
-- sonuçta BİR kez görünür, dolayısıyla ayıklama (dedup) gerekmez.
-- name: LockCustomersByIDOrEmailForErasure :many
SELECT id FROM customer
WHERE id = sqlc.arg('id') OR email = sqlc.arg('email')
ORDER BY id
FOR UPDATE;

-- AnonymizeCustomer müşterinin ADLI kişisel sütunlarını üzerine yazar.
--
-- Yazılmayan üç sütun ayrı ayrı düşünülmüştür:
--
--   - metadata SERBEST BİÇİMLİDİR ve gobit onu asla yeniden yazmaz (ADR 0029);
--     içinde kişisel veri olup olmadığına karar vermek gömen uygulamanındır.
--     Sonuç raporu bu yüzden onu Kept alanında bildirmek ZORUNDADIR.
--   - deleted_at'e dokunulmaz: unutulma bir silme değildir ve silinmiş bir
--     kaydı diriltmek de silinmemiş bir kaydı silmek de bu işin parçası değil.
--   - has_account korunur; hesap olup olmadığı kişiyi tanımlamaz ama kısmi
--     benzersizlik indeksinin kapsamını belirler.
--
-- e-posta parametre olarak GELİR, SQL içinde türetilmez: kuralın tek kaynağı
-- models.AnonymousEmail'dir ve burada tekrarlansaydı iki dil aynı kuralı
-- ayrı ayrı taşır, biri değiştiğinde ötekini sessizce yanlışlardı.
--
-- WHERE'in ikinci yarısı sayının DOĞRULUĞU içindir ve AnonymizeAddressesOfCustomer
-- ile AYNI kuralı uygular: satır ancak GERÇEKTEN bir şey taşıyorsa yazılır.
-- Koşul dört sütuna birden bakar, yalnızca e-postaya DEĞİL. Aradaki fark
-- ölçülebilir: anonimleştirilmiş kayıt SİLİNMEZ, canlı kalır ve UpdateCustomer
-- sütun sütun COALESCE eden bir yamadır — yönetici ya da vitrin, e-postaya hiç
-- dokunmadan taze bir first_name ve phone yazabilir. Yalnızca e-postaya bakan
-- bir koşul o satırı "zaten anonim" sayıp UPDATE'i tümüyle atlar ve kişinin adı
-- satırda dururken rapor "anonimleştirildi" derdi.
-- name: AnonymizeCustomer :execrows
UPDATE customer SET
    email      = sqlc.arg('email'),
    first_name = '',
    last_name  = '',
    phone      = '',
    updated_at = sqlc.arg('updated_at')
WHERE id = sqlc.arg('id')
  AND (email      <> sqlc.arg('email')
    OR first_name <> ''
    OR last_name  <> ''
    OR phone      <> '');

-- AnonymizeAddressesOfCustomer müşterinin TÜM adres satırlarını anonimleştirir.
--
-- Adresler yumuşak silinmiş olsalar bile kapsama girer: silinmiş bir adres
-- satırı da kişinin sokağını ve telefonunu taşır.
--
-- country_code KORUNUR. Ülke kodu kişiyi değil YARGI ALANINI adlandırır (vergi
-- ve saklama süresi oradan okunur) ve tek başına milyonlarca insana işaret eder.
-- Zaten boşaltılamazdı da: customer_address_country_check kodun tam iki BÜYÜK
-- harf olmasını ister. Korunduğu için sonuç raporunun Kept alanında bildirilir.
--
-- address_1 ve city BOŞALTILAMAZ (NOT NULL + CHECK <> ''), yerlerine yer tutucu
-- yazılır; geri kalan adlı sütunların hepsi boş dizeye çekilir.
--
-- WHERE'in ikinci yarısı sayının DOĞRULUĞU içindir: zaten anonim olan satır
-- güncellenmez. Koşul olmasaydı ikinci bir unutulma isteği hiçbir şey
-- değiştirmediği hâlde aynı satırları yeniden "yazıldı" diye sayar ve
-- updated_at'i ilerletirdi — yapılmamış bir işin makbuzu.
-- name: AnonymizeAddressesOfCustomer :execrows
UPDATE customer_address SET
    first_name  = '',
    last_name   = '',
    company     = '',
    address_1   = sqlc.arg('placeholder'),
    address_2   = '',
    city        = sqlc.arg('placeholder'),
    postal_code = '',
    phone       = '',
    updated_at  = sqlc.arg('updated_at')
WHERE customer_id = sqlc.arg('customer_id')
  AND (first_name  <> ''
    OR last_name   <> ''
    OR company     <> ''
    OR address_1   <> sqlc.arg('placeholder')
    OR address_2   <> ''
    OR city        <> sqlc.arg('placeholder')
    OR postal_code <> ''
    OR phone       <> '');
