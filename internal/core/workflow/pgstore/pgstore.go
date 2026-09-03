// Package pgstore workflow yürütme durumunu PostgreSQL'de kalıcılaştırır.
//
// Paket [workflow.Store] arayüzünü uygular. Arayüzü TÜKETEN taraf (motor)
// tanımlar, bu paket yalnızca imzayı karşılar — ADR 0001'in tüketici tarafı
// interface örüntüsü. Motor bu paketi import etmez; somut depo container'dan
// çözülür.
//
// Şema iki tablodur: workflow_executions (yürütmenin kendisi) ve
// workflow_execution_steps (adım kayıtları). Migration'lar pakete gömülüdür;
// çekirdek onları db.Migrate ile uygular (bkz. [Migrations], [MigrationOwner]).
//
// Paket genelinde geçerli üç kural:
//
//   - Yürütme girdileri ve çıktıları İŞ VERİSİDİR; hiçbir log kaydında
//     görünmezler (plan Bölüm 8, "hassas veri loglanmaz"). Loglar yalnızca
//     kimlik, workflow adı ve durum taşır.
//   - Bütün değerler sorgulara PARAMETRE olarak geçer; hiçbir SQL dizgesi
//     çalışma zamanı verisiyle birleştirilmez.
//   - Dışarı çıkan her hata core/errors ile tiplidir; ham sürücü hatası
//     zincirde kalır ve errors.Is/As ile erişilebilir.
package pgstore

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bdrtr/gobit/internal/core/db"
	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/workflow"
)

// Hata kodları; çağıran taraf errors.CodeOf ile bunlara göre dallanabilir.
const (
	// CodeInvalid girdinin depoya yazılamayacak durumda olduğunu bildirir.
	CodeInvalid = "workflow_store_invalid"
	// CodeNotFound istenen yürütmenin bulunmadığını bildirir.
	CodeNotFound = "workflow_execution_not_found"
	// CodeDuplicateKey aynı (workflow, idempotency_key) çiftinin zaten
	// kullanıldığını bildirir. Motor idempotent tekrarı bu kodla tanır ve
	// depodan Conflict sınıfıyla dönen TEK arıza budur (bkz. createError).
	CodeDuplicateKey = "workflow_execution_duplicate_key"
	// CodeDuplicateID aynı kimlikle ikinci kez kayıt açıldığını bildirir.
	// Sınıfı Invalid'dir: aynı kimliği iki kez vermek bir tekrar isteği değil,
	// çağıranın girdi hatasıdır.
	CodeDuplicateID = "workflow_execution_duplicate_id"
	// CodeConflict tanınmayan bir benzersizlik ihlalini bildirir; şema koddaki
	// varsayımdan sapmıştır. Sınıfı Internal'dır — ne olduğu bilinmeden
	// Conflict demek motoru tekrar yoluna sokardı.
	CodeConflict = "workflow_store_conflict"
	// CodeQueryFailed sorgunun sürücü seviyesinde başarısız olduğunu bildirir.
	CodeQueryFailed = "workflow_store_query_failed"
	// CodeUnavailable veritabanı havuzunun kurulmamış olduğunu bildirir.
	CodeUnavailable = "workflow_store_unavailable"
	// CodeCanceled bağlamın iş bitmeden iptal edildiğini bildirir.
	CodeCanceled = "workflow_store_canceled"
)

// Log alanlarında ve hata ayrıntılarında kullanılan anahtarlar.
const (
	keyExecutionID = "execution_id"
	keyWorkflow    = "workflow"
	keyStatus      = "status"
	keyStepIndex   = "step_index"
)

// store [workflow.Store] arayüzünün PostgreSQL uygulamasıdır.
// Eşzamanlı kullanıma güvenlidir; durumu yalnızca havuz ve logger'dır.
type store struct {
	pool *db.Pool
	log  *slog.Logger
}

// store'un sözleşmeyi karşıladığı derleme zamanında doğrulanır.
var _ workflow.Store = (*store)(nil)

// New verilen havuz üzerinde çalışan bir yürütme deposu döner.
//
// log nil ise loglama yapılmaz. Havuz nil ise New yine de bir depo döner;
// her metot o zaman KindUnavailable sınıfında tipli hata verir — yapıcının
// hata dönmemesi sözleşmenin (func New(...) workflow.Store) gereğidir.
func New(pool *db.Pool, log *slog.Logger) workflow.Store {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &store{pool: pool, log: log}
}

// Create yeni yürütme kaydı açar.
//
// Boş bırakılan alanları doldurur ve çağıranın struct'ına geri yazar:
// ID boşsa "wfx_" önekli yeni bir kimlik üretilir (bkz. newExecutionID),
// Status boşsa StatusRunning kullanılır, CreatedAt/UpdatedAt veritabanı
// saatinden alınır. Çağıranın verdiği zaman damgaları yok sayılır: birden çok
// replika yazarken tek doğru saat veritabanınınkidir.
//
// exec.Steps YOK SAYILIR; adım kayıtları yalnızca AppendStep ile eklenir.
//
// Aynı (Workflow, IdempotencyKey) çifti zaten varsa errors.Conflict döner
// (kod: CodeDuplicateKey). Bu karar SELECT ile değil, kısmi benzersiz indeksin
// ihlali yakalanarak verilir; iki süreç aynı anda çağırdığında yalnızca birinin
// başarılı olması bu yüzden garantidir. Conflict sınıfı YALNIZCA bu duruma
// ayrılmıştır: aynı KİMLİĞİN ikinci kez verilmesi errors.Invalid döner
// (kod: CodeDuplicateID), çünkü o bir tekrar isteği değil girdi hatasıdır
// (bkz. createError).
//
// IdempotencyKey boş dizeyse "anahtar yok" sayılır; yalnızca boşluktan oluşan
// bir anahtar ise errors.Invalid döner — sessizce anahtarsız sayılsaydı
// çağıranın istediği tekrar koruması hiçbir uyarı vermeden kaybolurdu.
//
// exec.Failure yazılabilir hâle GETİRİLİR (NUL baytı ve geçersiz UTF-8
// dizileri atılır, bkz. safeText) ve temizlenmiş hâli çağıranın struct'ına
// geri yazılır.
func (s *store) Create(ctx context.Context, exec *workflow.Execution) error {
	if exec == nil {
		return errors.Invalid(CodeInvalid, "yürütme kaydı nil olamaz")
	}

	name, err := requireText(exec.Workflow, "workflow adı", maxNameLen)
	if err != nil {
		return err
	}

	status := exec.Status
	if strings.TrimSpace(string(status)) == "" {
		status = workflow.StatusRunning
	}
	statusText, err := requireText(string(status), "durum", maxNameLen)
	if err != nil {
		return err
	}

	id := strings.TrimSpace(exec.ID)
	if id == "" {
		id = newExecutionID(time.Now())
	} else {
		id, err = requireText(id, "yürütme kimliği", maxIDLen)
		if err != nil {
			return err
		}
	}

	key, err := keyParam(exec.IdempotencyKey)
	if err != nil {
		return err
	}

	input, err := jsonParam(exec.Input, "input")
	if err != nil {
		return err
	}
	output, err := jsonParam(exec.Output, "output")
	if err != nil {
		return err
	}

	pool, err := s.rawPool()
	if err != nil {
		return err
	}

	failure := safeText(exec.Failure)

	var createdAt, updatedAt time.Time
	err = pool.QueryRow(ctx, insertExecutionSQL,
		id, name, key, statusText, input, output, failure,
	).Scan(&createdAt, &updatedAt)
	if err != nil {
		return createError(err, id, name, exec.IdempotencyKey)
	}

	exec.ID = id
	exec.Workflow = name
	exec.Status = workflow.Status(statusText)
	exec.Failure = failure
	exec.CreatedAt = createdAt.UTC()
	exec.UpdatedAt = updatedAt.UTC()

	s.log.DebugContext(ctx, "workflow yürütmesi açıldı",
		slog.String(keyExecutionID, exec.ID),
		slog.String(keyWorkflow, exec.Workflow),
		slog.String(keyStatus, statusText),
	)
	return nil
}

// FindByIdempotencyKey anahtara karşılık gelen yürütmeyi döner; yoksa
// errors.NotFound.
//
// Dönen kayıt adımlarını da taşır (Get ile aynı biçimde, Index sırasında):
// motor idempotent tekrarda yalnızca sonucu değil, nerede kalındığını da
// görebilmelidir.
//
// Anahtar Create'in kabul ettiği kümeden gelmelidir: boş anahtar aranamaz —
// depoda NULL olarak durur ve hiçbir kaydı tekil olarak seçmez — ve yazma
// yolunun reddettiği bir anahtar (yalnızca boşluk, sınırı aşan uzunluk) burada
// da errors.Invalid döner. İki yolun kabul kümesi ayrılsaydı, yazılabilen bir
// anahtar geri okunamaz ya da okunabilen bir anahtar hiç yazılamazdı.
func (s *store) FindByIdempotencyKey(ctx context.Context, wf, key string) (*workflow.Execution, error) {
	name, err := requireText(wf, "workflow adı", maxNameLen)
	if err != nil {
		return nil, err
	}
	aranan, err := keyParam(key)
	if err != nil {
		return nil, err
	}
	if aranan == nil {
		return nil, errors.Invalid(CodeInvalid, "idempotency anahtarı boş olamaz")
	}

	exec, err := s.queryExecution(ctx, selectByKeySQL, name, *aranan)
	if err != nil {
		return nil, err
	}
	if exec == nil {
		return nil, errors.NotFound(CodeNotFound,
			"%s workflow'unda %q idempotency anahtarlı yürütme yok", name, key).
			WithDetails(map[string]any{keyWorkflow: name})
	}
	return exec, nil
}

// AppendStep bir adım kaydını ekler ya da aynı Index'li kaydı günceller.
//
// Güncelleme yolu retry içindir: aynı adım yeniden denendiğinde ikinci bir
// satır açılmaz, var olan kayıt (Attempts dahil) üzerine yazılır. Çağrı ayrıca
// yürütmenin UpdatedAt'ini tazeler.
//
// rec.Failure yazılabilir hâle GETİRİLİR (bkz. safeText); tanı metni yüzünden
// adımın izinin hiç yazılamaması, hatanın kendisinden daha kötüdür.
//
// Yürütme yoksa errors.NotFound döner.
func (s *store) AppendStep(ctx context.Context, executionID string, rec workflow.StepRecord) error {
	id, err := requireText(executionID, "yürütme kimliği", maxIDLen)
	if err != nil {
		return err
	}
	stepName, err := requireText(rec.Name, "adım adı", maxNameLen)
	if err != nil {
		return err
	}
	stepStatus, err := requireText(string(rec.Status), "adım durumu", maxNameLen)
	if err != nil {
		return err
	}
	index, err := requireCount(rec.Index, "adım sırası (Index)")
	if err != nil {
		return err
	}
	attempts, err := requireCount(rec.Attempts, "deneme sayısı (Attempts)")
	if err != nil {
		return err
	}
	output, err := jsonParam(rec.Output, "adım çıktısı")
	if err != nil {
		return err
	}

	pool, err := s.rawPool()
	if err != nil {
		return err
	}

	tag, err := pool.Exec(ctx, upsertStepSQL,
		id, index, stepName, stepStatus, output, safeText(rec.Failure), attempts,
		timeParam(rec.StartedAt), timeParam(rec.EndedAt),
	)
	if err != nil {
		return wrapDB(err, CodeQueryFailed,
			"%q yürütmesinin %d numaralı adımı yazılamadı", id, rec.Index)
	}
	if tag.RowsAffected() == 0 {
		// Yabancı anahtar bu duruma normalde izin vermez; kısıt bir gün
		// düşerse adımın sahipsiz yazıldığı sessizce geçmesin diye kontrol
		// edilir.
		return errors.NotFound(CodeNotFound,
			"%q kimlikli yürütme yok; adım yazılamadı", id).
			WithDetails(map[string]any{keyExecutionID: id, keyStepIndex: rec.Index})
	}

	s.log.DebugContext(ctx, "workflow adımı yazıldı",
		slog.String(keyExecutionID, id),
		slog.Int(keyStepIndex, rec.Index),
		slog.String(keyStatus, stepStatus),
	)
	return nil
}

// UpdateStatus yürütmenin son durumunu yazar.
//
// output nil ise sütun NULL'a çekilir; failure boş dizeyse arıza açıklaması
// temizlenir. failure yazılabilir hâle GETİRİLİR (bkz. safeText): uç durumun
// yazılması, açıklamanın bozulmamasından önce gelir — yazılamayan bir uç durum
// kaydı sonsuza dek "running" bırakırdı.
//
// Yürütme yoksa errors.NotFound döner.
func (s *store) UpdateStatus(
	ctx context.Context,
	executionID string,
	status workflow.Status,
	output json.RawMessage,
	failure string,
) error {
	id, err := requireText(executionID, "yürütme kimliği", maxIDLen)
	if err != nil {
		return err
	}
	statusText, err := requireText(string(status), "durum", maxNameLen)
	if err != nil {
		return err
	}
	outputParam, err := jsonParam(output, "output")
	if err != nil {
		return err
	}

	pool, err := s.rawPool()
	if err != nil {
		return err
	}

	// Telafi eksiksiz tamamlandıysa yürütme dünyada iz BIRAKMAMIŞTIR; anahtar
	// da bir izdir ve bırakılır. Gerekçe [workflow.StatusFailed] godoc'unda.
	tag, err := pool.Exec(ctx, updateStatusSQL, id, statusText, outputParam, safeText(failure),
		status == workflow.StatusFailed)
	if err != nil {
		return wrapDB(err, CodeQueryFailed, "%q yürütmesinin durumu yazılamadı", id)
	}
	if tag.RowsAffected() == 0 {
		return errors.NotFound(CodeNotFound, "%q kimlikli yürütme yok", id).
			WithDetails(map[string]any{keyExecutionID: id})
	}

	s.log.DebugContext(ctx, "workflow yürütmesinin durumu güncellendi",
		slog.String(keyExecutionID, id),
		slog.String(keyStatus, statusText),
	)
	return nil
}

// Get yürütmeyi adımlarıyla birlikte okur; yoksa errors.NotFound.
// Adımlar Index'e göre artan sırada döner.
func (s *store) Get(ctx context.Context, executionID string) (*workflow.Execution, error) {
	id, err := requireText(executionID, "yürütme kimliği", maxIDLen)
	if err != nil {
		return nil, err
	}

	exec, err := s.queryExecution(ctx, selectByIDSQL, id)
	if err != nil {
		return nil, err
	}
	if exec == nil {
		return nil, errors.NotFound(CodeNotFound, "%q kimlikli yürütme yok", id).
			WithDetails(map[string]any{keyExecutionID: id})
	}
	return exec, nil
}

// queryExecution tek yürütme seçen bir sorguyu çalıştırır ve satırları
// yürütme + adım listesine katlar.
//
// Kayıt bulunamazsa (nil, nil) döner: "yok" durumunun mesajı çağrı yoluna
// göre değiştiği için hatayı çağıran üretir.
func (s *store) queryExecution(ctx context.Context, sql string, args ...any) (*workflow.Execution, error) {
	pool, err := s.rawPool()
	if err != nil {
		return nil, err
	}

	rows, err := pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, wrapDB(err, CodeQueryFailed, "yürütme okunamadı")
	}
	defer rows.Close()

	exec, err := foldRows(rows)
	if err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, wrapDB(err, CodeQueryFailed, "yürütme satırları okunamadı")
	}
	return exec, nil
}

// rowSource foldRows'un ihtiyaç duyduğu en küçük okuma yüzeyidir; pgx.Rows onu
// karşılar. Dar arayüz, katlama mantığının veritabanı olmadan sınanmasını
// sağlar (ADR 0001: arayüzü TÜKETEN taraf tanımlar).
type rowSource interface {
	Next() bool
	Scan(dest ...any) error
}

// foldRows birleşim satırlarını tek bir yürütmeye katlar.
//
// Yürütme sütunları YALNIZCA İLK SATIRDA taranır; gerekçesi
// [skipExecColumns] içindedir. Hiç satır yoksa (nil, nil) döner.
func foldRows(rows rowSource) (*workflow.Execution, error) {
	var (
		exec     *workflow.Execution
		row      execRow
		step     stepRow
		hedefler = scanTargets(&row, &step)
	)
	for rows.Next() {
		// Hedefler satırlar arasında paylaşıldığı için adım alanları
		// sıfırlanır: sürücü NULL sütunda hedefi zaten nil'ler, ama bu
		// varsayıma yaslanmak sessiz bir kopyalama hatasına açık kapı olurdu.
		step = stepRow{}
		if err := rows.Scan(hedefler...); err != nil {
			return nil, wrapDB(err, CodeQueryFailed, "yürütme satırı çözümlenemedi")
		}

		if exec == nil {
			exec = row.execution()
			skipExecColumns(hedefler)
		}
		// LEFT JOIN'de adımı olmayan yürütme tek satırla, adım sütunları NULL
		// olarak gelir; step_index NULL ise ortada adım yoktur.
		if step.index != nil {
			exec.Steps = append(exec.Steps, step.record())
		}
	}
	return exec, nil
}

// execColumnCount birleşim satırındaki yürütme sütunlarının sayısıdır.
const execColumnCount = 9

// scanTargets bir birleşim satırının tarama hedeflerini kurar.
//
// Sıra selectExecutionSQL'deki sütun sırasıdır; ikisi birlikte değişmelidir.
func scanTargets(row *execRow, step *stepRow) []any {
	return []any{
		&row.id, &row.name, &row.key, &row.status, &row.input, &row.output, &row.failure,
		&row.createdAt, &row.updatedAt,
		&step.index, &step.name, &step.status, &step.output, &step.failure, &step.attempts,
		&step.startedAt, &step.endedAt,
	}
}

// skipExecColumns yürütme sütunlarının tarama hedeflerini boşaltır.
//
// LEFT JOIN yürütme satırını her adım için TEKRAR taşır; ilk satırdan sonrası
// aynı verinin kopyasıdır. pgx nil hedefi atlar (Rows.Scan: "nil will skip the
// value entirely"), yani 100 KB'lık bir girdi yirmi adımlı bir yürütmede yirmi
// kez yeniden ayrılıp anında çöpe atılmaz. Ölçüldü: 256 KB girdisi ve sekiz
// adımı olan bir kaydın Get'i 2,17 MB yerine 0,28 MB ayırıyor.
//
// Sütunlar yine de tel üzerinden gelir: bu bedel, yürütme ile adımlarını TEK
// ifadede (tek anlık görüntüde) okumanın karşılığıdır — bkz. selectExecutionSQL.
func skipExecColumns(targets []any) {
	for i := range execColumnCount {
		targets[i] = nil
	}
}

// rawPool ham pgx havuzunu döner; havuz kurulmamışsa tipli hata üretir.
func (s *store) rawPool() (*pgxpool.Pool, error) {
	// db.Pool.Pool() nil alıcıya karşı güvenlidir; nil havuz nil döner.
	pool := s.pool.Pool()
	if pool == nil {
		return nil, errors.Unavailable(CodeUnavailable,
			"workflow deposu için veritabanı havuzu kurulmamış")
	}
	return pool, nil
}

// execRow workflow_executions satırının ham okuma biçimidir.
type execRow struct {
	id        string
	name      string
	key       *string
	status    string
	input     []byte
	output    []byte
	failure   string
	createdAt time.Time
	updatedAt time.Time
}

// execution ham satırı sözleşmedeki yürütme tipine çevirir.
func (r execRow) execution() *workflow.Execution {
	return &workflow.Execution{
		ID:             r.id,
		Workflow:       r.name,
		IdempotencyKey: keyValue(r.key),
		Status:         workflow.Status(r.status),
		Input:          jsonValue(r.input),
		Output:         jsonValue(r.output),
		Failure:        r.failure,
		CreatedAt:      r.createdAt.UTC(),
		UpdatedAt:      r.updatedAt.UTC(),
	}
}

// stepRow workflow_execution_steps satırının ham okuma biçimidir.
// Alanların işaretçi olması LEFT JOIN'den gelen NULL'ları taşımak içindir.
type stepRow struct {
	index     *int32
	name      *string
	status    *string
	output    []byte
	failure   *string
	attempts  *int32
	startedAt *time.Time
	endedAt   *time.Time
}

// record ham satırı sözleşmedeki adım kaydına çevirir.
// Yalnızca index dolu olduğunda çağrılır.
func (r stepRow) record() workflow.StepRecord {
	return workflow.StepRecord{
		Name:      textValue(r.name),
		Index:     int(*r.index),
		Status:    workflow.StepStatus(textValue(r.status)),
		Output:    jsonValue(r.output),
		Failure:   textValue(r.failure),
		Attempts:  countValue(r.attempts),
		StartedAt: timeValue(r.startedAt),
		EndedAt:   timeValue(r.endedAt),
	}
}

// textValue NULL metni boş dizeye çevirir.
func textValue(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

// countValue NULL sayacı sıfıra çevirir.
func countValue(v *int32) int {
	if v == nil {
		return 0
	}
	return int(*v)
}
