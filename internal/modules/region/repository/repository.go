// Package repository region modülünün veritabanı erişim katmanıdır.
//
// sqlc'nin ürettiği regiondb paketi bu paketin İÇİNDE kalır: dışarıya yalnızca
// [models] domain tipleri verilir, pgtype hiçbir imzada görünmez. Bu sınır
// bilinçlidir — servis ve API katmanları depolama ayrıntısına bağlanmaz ve
// üretilen kod yeniden üretildiğinde yalnızca bu paket etkilenir.
//
// Ham hatalar da sınırı geçmez: pgx.ErrNoRows ve PostgreSQL kısıt ihlalleri
// burada core/errors'ın tipli hatalarına çevrilir, böylece HTTP katmanı
// status kodunu doğru seçer (plan Bölüm 2.7).
//
// # Kilit sırası
//
// Bölge ve ülke satırlarına birlikte dokunan her akış kilitleri AYNI SIRADA
// alır: ÖNCE bölge, SONRA ülke. Sıra tek olduğu için iki akış birbirini
// bekleyecek biçimde kilitlenemez (deadlock). Sırayı bozan tek bir sorgu bile
// yeterlidir, bu yüzden kilit alan sorgular queries/ altında ayrıca
// belgelenmiştir.
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
	"github.com/bdrtr/gobit/internal/modules/region/models"
	"github.com/bdrtr/gobit/internal/modules/region/repository/regiondb"
)

// Hata kodları; çağıran taraf errors.CodeOf ile bunlara bakabilir.
const (
	// CodeRegionNotFound istenen bölgenin bulunamadığını bildirir.
	CodeRegionNotFound = "region_not_found"
	// CodeCountryNotFound istenen ülkenin bulunamadığını bildirir.
	CodeCountryNotFound = "country_not_found"
	// CodeCurrencyNotFound istenen para biriminin bulunamadığını bildirir.
	CodeCurrencyNotFound = "currency_not_found"
	// CodeUnknownCurrency bölgeye tanımsız bir para birimi verildiğini bildirir.
	CodeUnknownCurrency = "region_unknown_currency"
	// CodeCountryTaken ülkenin başka bir bölgeye ait olduğunu bildirir.
	CodeCountryTaken = "country_already_in_region"
	// CodeCountryNotInRegion ülkenin o bölgeye ait olmadığını bildirir.
	CodeCountryNotInRegion = "country_not_in_region"
	// CodeConstraintViolation veritabanı kısıtının ihlal edildiğini bildirir.
	CodeConstraintViolation = "region_constraint_violation"
	// CodeDuplicate benzersizlik ihlalini bildirir.
	CodeDuplicate = "region_duplicate"
	// CodeQueryFailed beklenmeyen bir veritabanı hatasını bildirir.
	CodeQueryFailed = "region_query_failed"
	// CodeCanceled bağlam iptalini bildirir.
	CodeCanceled = "region_canceled"
	// CodeTxFailed işlem (transaction) yönetiminin başarısızlığını bildirir.
	CodeTxFailed = "region_tx_failed"
)

// PostgreSQL SQLSTATE kodları (ihtiyaç duyulanlar).
const (
	sqlstateCheckViolation       = "23514"
	sqlstateUniqueViolation      = "23505"
	sqlstateForeignKeyViolation  = "23503"
	sqlstateNotNullViolation     = "23502"
	sqlstateStringDataRightTrunc = "22001"
)

// constraintRegionCurrency bölgeyi para birimine bağlayan foreign key'in
// migration'daki adıdır. Sürücü hatasını anlamlı bir mesaja çevirmek için
// kullanılır; ad migration ile birebir aynı olmalıdır.
const constraintRegionCurrency = "region_currency_fk"

// Repo region tablolarına erişimi sağlar. Eşzamanlı kullanıma güvenlidir.
type Repo struct {
	pool *pgxpool.Pool
	q    *regiondb.Queries
}

// New verilen havuz üzerinde çalışan bir depo üretir.
//
// pool nil ise bu, kurulumda değil ilk çağrıda tipli bir hata olarak bildirilir;
// kurulum yolu panik üretmez.
func New(pool *pgxpool.Pool) *Repo {
	r := &Repo{pool: pool}
	if pool != nil {
		r.q = regiondb.New(pool)
	}
	return r
}

// ready havuzun kullanılabilir olduğunu doğrular.
func (r *Repo) ready() error {
	if r == nil || r.pool == nil || r.q == nil {
		return errors.Unavailable(CodeQueryFailed, "region veritabanı havuzu kurulmamış")
	}
	return nil
}

// inTx fn'i tek bir işlemde çalıştırır; fn hata dönerse işlem GERİ ALINIR.
//
// Kilit alan akışlar için zorunludur: FOR UPDATE / FOR SHARE kilidi işlem
// bitince serbest kalır, yani işlemsiz bir kilit hiçbir şey korumazdı.
func (r *Repo) inTx(ctx context.Context, fn func(q *regiondb.Queries) error) error {
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
// Bölgenin para birimi foreign key'i ayrıca ele alınır: "bilinmeyen para
// birimi" istemcinin düzeltebileceği bir girdi hatasıdır ve mesajı kısıt adı
// değil, ne yapılması gerektiği olmalıdır.
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
		if pgErr.Code == sqlstateForeignKeyViolation && pgErr.ConstraintName == constraintRegionCurrency {
			return errors.Wrap(err, errors.KindInvalid, CodeUnknownCurrency,
				"%s: para birimi tanımlı değil", sprintf(format, a...))
		}
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

// toRegion üretilen satırı domain modeline çevirir.
func toRegion(row regiondb.Region) models.Region {
	return models.Region{
		ID:             row.ID,
		Name:           row.Name,
		CurrencyCode:   row.CurrencyCode,
		AutomaticTaxes: row.AutomaticTaxes,
		TaxRate:        row.TaxRate,
		CreatedAt:      toTime(row.CreatedAt),
		UpdatedAt:      toTime(row.UpdatedAt),
		DeletedAt:      toTimePtr(row.DeletedAt),
	}
}

// toCountry üretilen satırı domain modeline çevirir.
//
// RegionID işaretçisi KOPYALANIR: üretilen satırın işaretçisini olduğu gibi
// taşımak, çağıranın modeli değiştirmesiyle satırın da değişmesi demek olurdu.
func toCountry(row regiondb.Country) models.Country {
	country := models.Country{
		Code:      row.Iso2,
		Name:      row.Name,
		CreatedAt: toTime(row.CreatedAt),
		UpdatedAt: toTime(row.UpdatedAt),
	}
	if row.RegionID != nil {
		id := *row.RegionID
		country.RegionID = &id
	}
	return country
}

// toCurrency üretilen satırı domain modeline çevirir.
func toCurrency(row regiondb.Currency) models.Currency {
	return models.Currency{
		Code:          row.Code,
		Symbol:        row.Symbol,
		Name:          row.Name,
		DecimalDigits: row.DecimalDigits,
		CreatedAt:     toTime(row.CreatedAt),
		UpdatedAt:     toTime(row.UpdatedAt),
	}
}
