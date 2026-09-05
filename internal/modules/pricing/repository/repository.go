// Package repository pricing modülünün veritabanı erişim katmanıdır.
//
// sqlc'nin ürettiği pricingdb paketi bu paketin İÇİNDE kalır: dışarıya yalnızca
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
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/pricing/repository/pricingdb"
)

// Hata kodları; çağıran taraf errors.CodeOf ile bunlara bakabilir.
const (
	// CodePriceSetNotFound istenen price set'in bulunamadığını bildirir.
	CodePriceSetNotFound = "price_set_not_found"
	// CodePriceNotFound istenen fiyatın bulunamadığını bildirir.
	CodePriceNotFound = "price_not_found"
	// CodePriceListNotFound istenen fiyat listesinin bulunamadığını bildirir.
	CodePriceListNotFound = "price_list_not_found"
	// CodePriceRuleNotFound istenen fiyat kuralının bulunamadığını bildirir.
	CodePriceRuleNotFound = "price_rule_not_found"
	// CodeConstraintViolation veritabanı kısıtının ihlal edildiğini bildirir.
	CodeConstraintViolation = "pricing_constraint_violation"
	// CodeDuplicate benzersizlik ihlalini bildirir.
	CodeDuplicate = "pricing_duplicate"
	// CodeQueryFailed beklenmeyen bir veritabanı hatasını bildirir.
	CodeQueryFailed = "pricing_query_failed"
	// CodeCanceled bağlam iptalini bildirir.
	CodeCanceled = "pricing_canceled"
	// CodeTxFailed işlem (transaction) yönetiminin başarısızlığını bildirir.
	CodeTxFailed = "pricing_tx_failed"
)

// PostgreSQL SQLSTATE kodları (ihtiyaç duyulanlar).
const (
	sqlstateCheckViolation       = "23514"
	sqlstateUniqueViolation      = "23505"
	sqlstateForeignKeyViolation  = "23503"
	sqlstateNotNullViolation     = "23502"
	sqlstateStringDataRightTrunc = "22001"
)

// Repo pricing tablolarına erişimi sağlar. Eşzamanlı kullanıma güvenlidir.
type Repo struct {
	pool *pgxpool.Pool
	q    *pricingdb.Queries
}

// New verilen havuz üzerinde çalışan bir depo üretir.
//
// pool nil ise bu, kurulumda değil ilk çağrıda tipli bir hata olarak bildirilir;
// kurulum yolu panik üretmez.
func New(pool *pgxpool.Pool) *Repo {
	r := &Repo{pool: pool}
	if pool != nil {
		r.q = pricingdb.New(pool)
	}
	return r
}

// ready havuzun kullanılabilir olduğunu doğrular.
func (r *Repo) ready() error {
	if r == nil || r.pool == nil || r.q == nil {
		return errors.Unavailable(CodeQueryFailed, "pricing veritabanı havuzu kurulmamış")
	}
	return nil
}

// inTx fn'i tek bir işlemde çalıştırır; fn hata dönerse işlem GERİ ALINIR.
//
// Atomiklik SetPrices için zorunludur: bir price set'in fiyatları topluca
// yazılırken eski fiyatların silinmesiyle yenilerinin eklenmesi arasında hata
// oluşursa, kap fiyatsız kalırdı. İşlem, kabın ya eski ya yeni fiyat kümesiyle
// görünmesini garanti eder.
func (r *Repo) inTx(ctx context.Context, fn func(q *pricingdb.Queries) error) error {
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
			return errors.Wrap(err, errors.KindConflict, CodeDuplicate,
				"%s (kısıt: %s)", sprintf(format, a...), pgErr.ConstraintName)
		case sqlstateCheckViolation, sqlstateForeignKeyViolation,
			sqlstateNotNullViolation, sqlstateStringDataRightTrunc:
			return errors.Wrap(err, errors.KindInvalid, CodeConstraintViolation,
				"%s (kısıt: %s)", sprintf(format, a...), pgErr.ConstraintName)
		}
	}

	return errors.Wrap(err, errors.KindInternal, CodeQueryFailed, format, a...)
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

// fromTimePtr isteğe bağlı bir zamanı damgaya çevirir; nil ise SQL NULL.
func fromTimePtr(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return fromTime(*t)
}
