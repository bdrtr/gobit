-- file_uploads sorguları — YÜKLEME DEFTERİ.
--
-- Defter, dosyanın kendisinden SONRA yazılır: önce sağlayıcıya yazılır, sonra
-- kayıt açılır. Ters sıra — önce kayıt, sonra yazma — kaydın işaret ettiği
-- dosyanın hiç var olmadığı bir pencere bırakırdı ve o pencerede sunulan her
-- adres 404 dönerdi. Bu sırayla oluşan tek tutarsızlık ise DOSYASI olan ama
-- kaydı olmayan bir nesnedir: erişilemez, ama hiçbir şeyi bozmaz.

-- name: CreateFileUpload :one
INSERT INTO file_uploads (
    id, storage_key, provider_id, content_type, size, checksum,
    original_name, url, uploaded_by
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: GetFileUpload :one
SELECT * FROM file_uploads
WHERE id = $1;

-- GetFileUploadByKey SUNUM yolunun tek sorgusudur.
--
-- Adres çubuğundan gelen anahtar önce BURAYA sorulur; satır yoksa dosya
-- sistemine hiç dokunulmaz. Böylece diske ulaşabilen tek anahtar, bu modülün
-- kendi ürettiği ve deftere yazdığı anahtardır — sunulan şeyin yalnızca
-- yüklenmiş dosyalar olduğunu garanti eden şey budur.
-- name: GetFileUploadByKey :one
SELECT * FROM file_uploads
WHERE storage_key = $1;

-- name: ListFileUploads :many
SELECT * FROM file_uploads
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('row_limit')::bigint OFFSET sqlc.arg('row_offset')::bigint;

-- CountFileUploads sayfalama zarfının toplam sayısını verir.
--
-- Toplam, satırlarla birlikte dönen bir pencere fonksiyonundan okunamaz:
-- aralık dışı bir sayfada hiç satır dönmez, pencere değerlendirilmez ve toplam
-- 0 görünürdü.
-- name: CountFileUploads :one
SELECT COUNT(*) FROM file_uploads;

-- DeleteFileUpload kaydı KALICI olarak siler ve kaç satır sildiğini bildirir.
--
-- Sıfır satır bir hata DEĞİLDİR ve çağıran da onu hata saymaz: silme bir SON
-- DURUM iddiasıdır ("bu yükleme artık yok") ve yeniden denenen bir temizlik
-- akışının ikinci turunda satır zaten gitmiş olur.
-- name: DeleteFileUpload :execrows
DELETE FROM file_uploads
WHERE id = $1;
