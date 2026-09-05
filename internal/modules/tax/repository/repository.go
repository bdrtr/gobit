// Package repository tax modülünün veritabanı erişim katmanıdır.
//
// sqlc'nin ürettiği taxdb paketi bu paketin İÇİNDE kalır: dışarıya yalnızca
// [models] domain tipleri verilir, pgtype hiçbir imzada görünmez. Bu sınır
// bilinçlidir — servis ve API katmanları depolama ayrıntısına bağlanmaz ve
// üretilen kod yeniden üretildiğinde yalnızca bu paket etkilenir.
//
// Ham hatalar da sınırı geçmez: pgx.ErrNoRows ve PostgreSQL kısıt ihlalleri
// burada core/errors'ın tipli hatalarına çevrilir, böylece HTTP katmanı
// status kodunu doğru seçer (plan Bölüm 2.7).
package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/repository/taxdb"
)

// Hata kodları; çağıran taraf errors.CodeOf ile bunlara bakabilir.
const (
	// CodeTaxRegionNotFound istenen vergi bölgesinin bulunamadığını bildirir.
	CodeTaxRegionNotFound = "tax_region_not_found"
	// CodeTaxRateNotFound istenen vergi oranının bulunamadığını bildirir.
	CodeTaxRateNotFound = "tax_rate_not_found"
	// CodeTaxRateRuleNotFound istenen kuralın bulunamadığını bildirir.
	CodeTaxRateRuleNotFound = "tax_rate_rule_not_found"
	// CodeConstraintViolation veritabanı kısıtının ihlal edildiğini bildirir.
	CodeConstraintViolation = "tax_constraint_violation"
	// CodeDuplicate SERVİSTE karşılığı olmayan benzersizlik ihlallerini
	// bildirir (örn. aynı kuralın ikinci kez yazılması).
	//
	// Servisin kendi adı olan iki ihlal bu koda DÜŞMEZ: ülkeye ikinci kök
	// bölge ve bölgeye ikinci varsayılan oran, kısıt adına göre servis
	// koduna eşlenir (bkz. duplicateCode).
	CodeDuplicate = "tax_duplicate"
	// CodeRegionRootExists ülkenin zaten bir kök vergi bölgesi olduğunu
	// bildirir; service.CodeRootExists ile AYNI değerdir.
	CodeRegionRootExists = "tax_region_root_exists"
	// CodeDefaultRateExists bölgede zaten bir varsayılan oran olduğunu
	// bildirir; service.CodeDefaultExists ile AYNI değerdir.
	CodeDefaultRateExists = "tax_default_rate_exists"
	// CodeDataInvalid saklanan JSON alanının çözümlenemediğini bildirir.
	CodeDataInvalid = "tax_data_invalid"
	// CodeQueryFailed beklenmeyen bir veritabanı hatasını bildirir.
	CodeQueryFailed = "tax_query_failed"
	// CodeCanceled bağlam iptalini bildirir.
	CodeCanceled = "tax_canceled"
	// CodeTxFailed işlem (transaction) yönetiminin başarısızlığını bildirir.
	CodeTxFailed = "tax_tx_failed"
)

// PostgreSQL SQLSTATE kodları (ihtiyaç duyulanlar).
const (
	sqlstateCheckViolation       = "23514"
	sqlstateUniqueViolation      = "23505"
	sqlstateForeignKeyViolation  = "23503"
	sqlstateNotNullViolation     = "23502"
	sqlstateStringDataRightTrunc = "22001"
)

// Servis kurallarının veritabanındaki karşılığı olan kısıt adları. Adlar
// migration'daki indekslerle birebir aynıdır; buradaki bir yazım hatası
// eşlemeyi sessizce devre dışı bırakır ve kod yeniden ayrışırdı.
const (
	constraintRegionCountryRoot = "tax_region_country_root_uniq"
	constraintRateDefault       = "tax_rate_default_uniq"
)

// Repo tax tablolarına erişimi sağlar. Eşzamanlı kullanıma güvenlidir.
type Repo struct {
	pool *pgxpool.Pool
	q    *taxdb.Queries
}

// New verilen havuz üzerinde çalışan bir depo üretir.
//
// pool nil ise bu, kurulumda değil ilk çağrıda tipli bir hata olarak bildirilir;
// kurulum yolu panik üretmez.
func New(pool *pgxpool.Pool) *Repo {
	r := &Repo{pool: pool}
	if pool != nil {
		r.q = taxdb.New(pool)
	}
	return r
}

// ready havuzun kullanılabilir olduğunu doğrular.
func (r *Repo) ready() error {
	if r == nil || r.pool == nil || r.q == nil {
		return errors.Unavailable(CodeQueryFailed, "tax veritabanı havuzu kurulmamış")
	}
	return nil
}

// inTx fn'i tek bir işlemde çalıştırır; fn hata dönerse işlem GERİ ALINIR.
//
// Atomiklik silme yollarında zorunludur: bir bölge silinirken önce bölge, sonra
// oranları, sonra kuralları yumuşak silinir. Arada hata oluşursa bölge silinmiş
// ama oranları canlı kalırdı; o oranlar hiçbir hesaba girmez ama aynı ülkeye
// açılan yeni bir bölgenin yanında yetim satır olarak durur ve rapor
// toplamlarını bozardı.
func (r *Repo) inTx(ctx context.Context, fn func(q *taxdb.Queries) error) error {
	if err := r.ready(); err != nil {
		return err
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return wrapDB(err, "işlem başlatılamadı")
	}
	// Rollback, Commit'ten sonra çağrıldığında pgx.ErrTxClosed döner ve
	// yok sayılır; bu, başarılı yolda da defer'ın güvenle kalmasını sağlar.
	defer func() { _ = tx.Rollback(ctx) }()

	if err := fn(r.q.WithTx(tx)); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return wrapDB(err, "işlem tamamlanamadı")
	}
	return nil
}

// wrapDB ham bir veritabanı hatasını tipli hataya çevirir.
//
// Sınıflandırma bilinçlidir: kısıt ihlali İSTEMCİ hatasıdır (422), benzersizlik
// ihlali çakışmadır (409), iptal geçici erişilemezliktir (503); geri kalan her
// şey sunucu hatasıdır ve mesajı istemciye SIZDIRILMAZ (bkz. core/http).
//
// Kısıt ADI mesaja yazılır: "tax_region_country_root_uniq" ile
// "tax_rate_default_uniq" ihlalleri aynı sınıfa düşer ve yönetici hangi kuralı
// çiğnediğini ancak addan anlar. Ad ayrıca KODU da belirler (bkz.
// duplicateCode).
func wrapDB(err error, format string, a ...any) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return errors.Wrap(err, errors.KindUnavailable, CodeCanceled, format, a...)
	case errors.Is(err, pgx.ErrTxClosed), errors.Is(err, pgx.ErrTxCommitRollback):
		return errors.Wrap(err, errors.KindInternal, CodeTxFailed, format, a...)
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case sqlstateUniqueViolation:
			return errors.Wrap(err, errors.KindConflict, duplicateCode(pgErr.ConstraintName),
				"%s (kısıt: %s)", sprintf(format, a...), pgErr.ConstraintName)
		case sqlstateCheckViolation, sqlstateForeignKeyViolation,
			sqlstateNotNullViolation, sqlstateStringDataRightTrunc:
			return errors.Wrap(err, errors.KindInvalid, CodeConstraintViolation,
				"%s (kısıt: %s)", sprintf(format, a...), pgErr.ConstraintName)
		}
	}

	return errors.Wrap(err, errors.KindInternal, CodeQueryFailed, format, a...)
}

// duplicateCode benzersizlik ihlalini KISIT ADINA göre bir hata koduna eşler.
//
// Eşleme, aynı durumun iki yoldan aynı kodla dönmesini sağlar: servis "önce
// oku, sonra yaz" denetimine takılan istek [CodeRegionRootExists] alır, o
// denetimi geçip veritabanı indeksine çarpan (yarışı kaybeden) istek de aynı
// kodu almalıdır. İkisi ayrışsaydı, koda göre dallanan bir yönetim arayüzü
// eşzamanlı iki istekte aynı durumu iki farklı koddan görür ve mesajı yanlış
// eşlerdi — hata SINIFI (çakışma) her iki yolda aynı olduğu için fark
// testlerden de kaçardı.
//
// Adı bilinmeyen kısıtlar genel [CodeDuplicate]'e düşer: servis tarafında
// karşılığı olmayan bir ihlale uydurma bir ad vermek, çağıranı olmayan bir
// duruma dallandırırdı.
func duplicateCode(constraint string) string {
	switch constraint {
	case constraintRegionCountryRoot:
		return CodeRegionRootExists
	case constraintRateDefault:
		return CodeDefaultRateExists
	default:
		return CodeDuplicate
	}
}

// notFoundOr pgx.ErrNoRows'u NotFound'a, diğer her şeyi wrapDB'ye çevirir.
func notFoundOr(err error, code, format string, a ...any) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return errors.NotFound(code, format, a...)
	}
	return wrapDB(err, format, a...)
}

// sprintf hata mesajını bir kez biçimlendirir.
//
// Argümansız çağrılarda format DEĞİŞTİRİLMEDEN döner; aksi hâlde mesajdaki bir
// yüzde işareti (örn. "%!d(MISSING)") kullanıcıya bozuk metin olarak giderdi.
func sprintf(format string, a ...any) string {
	if len(a) == 0 {
		return format
	}
	return fmt.Sprintf(format, a...)
}

// toTime NULL olmayan bir zaman damgasını UTC time.Time'a çevirir.
//
// Geçersiz (NULL) damga sıfır zaman döner: NOT NULL sütunlarda bu durum
// oluşamaz, oluşursa da sıfır zaman panik üretmeyen ve testte göze batan bir
// değerdir.
func toTime(ts pgtype.Timestamptz) time.Time {
	if !ts.Valid {
		return time.Time{}
	}
	return ts.Time.UTC()
}

// toTimePtr NULL olabilen bir zaman damgasını *time.Time'a çevirir.
func toTimePtr(ts pgtype.Timestamptz) *time.Time {
	if !ts.Valid {
		return nil
	}
	t := ts.Time.UTC()
	return &t
}

// fromTime bir zamanı NOT NULL damgaya çevirir; daima UTC yazılır.
func fromTime(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
}

// toJSONMap jsonb sütununu haritaya çevirir.
//
// Boş ya da JSON null değer nil harita döner; böylece API yanıtında
// "metadata": null yerine alan hiç görünmez (omitempty).
func toJSONMap(raw []byte) (map[string]any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, CodeDataInvalid,
			"JSON alanı çözümlenemedi")
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// fromJSONMap haritayı jsonb sütununa yazılacak bayta çevirir.
//
// Boş harita "{}" yazar: sütun NOT NULL'dur ve "üstveri yok" ile "üstveri boş"
// ayrımı bu modülde bir şey ifade etmez.
func fromJSONMap(m map[string]any) ([]byte, error) {
	if len(m) == 0 {
		return []byte("{}"), nil
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInvalid, CodeDataInvalid,
			"JSON alanı kodlanamadı")
	}
	return raw, nil
}

// optionalText boş dizeyi SQL NULL'a, dolu dizeyi işaretçiye çevirir.
func optionalText(value string) *string {
	if value == "" {
		return nil
	}
	out := value
	return &out
}
