package pgstore

// PostgreSQL SQLSTATE kodları. (github.com/jackc/pgerrcode bağımlılığı eklemek
// yerine kullanılan kodlar sabit yazıldı; link paketi de aynı yolu izler.)
//
// Son üçü ÇAĞIRANIN VERİSİNDEN doğar: değer sütun tipine çevrilemez (metinde
// NUL baytı, JSON'da NUL kaçışı, bozuk UTF-8). Bu yüzden KindInvalid'e
// eşlenirler; bkz. wrapDB. Kodların gerçekten bunlar olduğu entegrasyon
// testinde canlı sunucuya sorularak doğrulanır.
const (
	// uniqueViolation benzersizlik ihlalidir (kimlik ya da idempotency çakışması).
	uniqueViolation = "23505"
	// foreignKeyViolation adımın bağlandığı yürütmenin bulunmadığını bildirir.
	foreignKeyViolation = "23503"
	// checkViolation şema düzeyindeki CHECK kısıtının ihlalidir.
	checkViolation = "23514"
	// notInRepertoire değerin sunucu kodlamasında karşılığı olmadığını bildirir
	// (örn. metindeki NUL baytı).
	notInRepertoire = "22021"
	// untranslatableCharacter JSONB'nin desteklemediği Unicode kaçışını
	// bildirir (NUL kaçışı metne çevrilemez).
	untranslatableCharacter = "22P05"
	// invalidTextRepresentation metnin hedef tipe ayrıştırılamadığını bildirir
	// (örn. eşi olmayan vekil çift taşıyan JSON).
	invalidTextRepresentation = "22P02"
)

// Hata eşlemesinde tanınan kısıt adları. Şemadaki karşılıkları
// migrations/000001_workflow_init.up.sql içindedir; adların gerçekten bu
// olduğu entegrasyon testinde katalogdan (pg_class) doğrulanır — yoksa
// eşleme sessizce genel dala düşerdi.
const (
	// executionsPKConstraint id sütunu üzerindeki birincil anahtardır.
	executionsPKConstraint = "workflow_executions_pkey"
	// idempotencyIndex (workflow, idempotency_key) kısmi benzersiz indeksidir.
	idempotencyIndex = "workflow_executions_idempotency_key_uniq"
)

// insertExecutionSQL yeni bir yürütme kaydı açar.
//
// Zaman damgalarını VERİTABANI saati üretir: birden çok replika aynı tabloya
// yazdığında uygulama saatleri arasındaki kayma kayıtların sırasını bozardı.
// RETURNING, yazılan gerçek değerleri geri verir; çağıranın struct'ı bunlarla
// doldurulur.
const insertExecutionSQL = `
INSERT INTO workflow_executions (
	id, workflow, idempotency_key, status, input, output, failure, created_at, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, now(), now())
RETURNING created_at, updated_at`

// upsertStepSQL adım kaydını ekler ya da aynı index'li kaydı günceller.
//
// Tek ifadedir: ekleme, veri değiştiren bir CTE içinde yapılır ve ana UPDATE
// yürütmenin updated_at'ini tazeler. Ayrı iki ifade seçilseydi araya düşen bir
// hata, adımı yazılmış ama yürütmeyi bayat updated_at ile bırakabilirdi.
//
// ON CONFLICT hedefi (execution_id, step_index) birincil anahtarıdır: retry
// sırasında aynı adım yeniden yazıldığında yeni satır AÇILMAZ, attempts dahil
// tüm alanlar güncellenir.
const upsertStepSQL = `
WITH yazilan AS (
	INSERT INTO workflow_execution_steps (
		execution_id, step_index, name, status, output, failure, attempts, started_at, ended_at
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	ON CONFLICT (execution_id, step_index) DO UPDATE SET
		name       = EXCLUDED.name,
		status     = EXCLUDED.status,
		output     = EXCLUDED.output,
		failure    = EXCLUDED.failure,
		attempts   = EXCLUDED.attempts,
		started_at = EXCLUDED.started_at,
		ended_at   = EXCLUDED.ended_at
	RETURNING execution_id
)
UPDATE workflow_executions e
SET updated_at = now()
FROM yazilan
WHERE e.id = yazilan.execution_id`

// updateStatusSQL yürütmenin son durumunu yazar.
// $5 idempotency anahtarının BIRAKILIP bırakılmayacağıdır.
//
// Karar Go tarafında verilir ve buraya boolean olarak gelir; SQL'e 'failed'
// dizesini gömmek, durum sabitinin ikinci bir kopyasını üretirdi ve iki kopya
// ayrıştığı gün kural sessizce çalışmaz olurdu.
//
// Anahtar NULL'a çekilir, SATIR SİLİNMEZ: başarısız deneme denetim kaydı olarak
// kalmalıdır. Kısmi tekil indeks yalnızca DOLU anahtarları kapsadığı için
// NULL'a çekilen satır bir sonraki denemenin önünü açar.
const updateStatusSQL = `
UPDATE workflow_executions
SET status = $2, output = $3, failure = $4, updated_at = now(),
    idempotency_key = CASE WHEN $5 THEN NULL ELSE idempotency_key END
WHERE id = $1`

// selectExecutionSQL yürütmeyi adımlarıyla birlikte TEK ifadede okur.
//
// LEFT JOIN bilinçlidir: adımı olmayan bir yürütme de tek satırla döner
// (adım sütunları NULL olur). İki ayrı sorgu seçilseydi aralarında araya
// giren bir yazma, yürütmeyi bir anın adımlarını başka bir anın hâliyle
// birleştirebilirdi; tek ifade tutarlı bir görüntü garanti eder.
//
// ORDER BY s.step_index, sözleşmenin "adımlar Index sırasına göre döner"
// koşulunu veritabanı tarafında karşılar.
const selectExecutionSQL = `
SELECT
	e.id, e.workflow, e.idempotency_key, e.status, e.input, e.output, e.failure,
	e.created_at, e.updated_at,
	s.step_index, s.name, s.status, s.output, s.failure, s.attempts,
	s.started_at, s.ended_at
FROM workflow_executions e
LEFT JOIN workflow_execution_steps s ON s.execution_id = e.id
`

// selectByIDSQL yürütmeyi kimliğiyle okur.
const selectByIDSQL = selectExecutionSQL + `WHERE e.id = $1
ORDER BY s.step_index`

// selectByKeySQL yürütmeyi (workflow, idempotency_key) çiftiyle okur.
// Çift, kısmi benzersiz indeks sayesinde en fazla bir satır seçer.
const selectByKeySQL = selectExecutionSQL + `WHERE e.workflow = $1 AND e.idempotency_key = $2
ORDER BY s.step_index`
