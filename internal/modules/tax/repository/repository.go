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
//
// # İşlemin taşınması
//
// [Repo.WithTx] bir işlem açar ve onu CONTEXT'e koyar; işlem sürerken o
// context ile çağrılan HER depo metodu aynı işlemde çalışır. Reddedilen
// alternatif, işlem tutamağını metot imzasına koymaktı (bu paket bunu bir
// süre yaptı: işlem yalnızca depo İÇİNDE, `func(q *taxdb.Queries) error`
// alan özel bir yardımcıyla açılıyordu). O biçim iki depo çağrısını TEK
// işlemde birleştirmeyi imkânsız kılar, çünkü tutamağı yalnızca bu paket
// üretebilir ve dışarıya veremez — servis, kuralını okuduğu satırla yazdığı
// satırı aynı işlemde tutamaz. İmzayı iki tarafın da paylaştığı tiplere
// (context.Context, models.*) indirmek, servisin KENDİ paketinde tanımladığı
// dar arayüzle bu paketin YAPISAL olarak eşleşmesini de sağlar; ADR 0001
// servisin bu paketi import etmesini yasakladığı için imzada bu paketin bir
// tipi geçemez.
//
// Kilit alan metot ([Repo.LockTaxRegion]) işlem DIŞINDA çağrılırsa hata
// döner: FOR SHARE kilidi işlem bitince serbest kalır, yani işlemsiz bir
// kilit hiçbir şey korumaz ve koruduğu sanılır.
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
	// CodeTxRequired kilit alan bir metodun işlem DIŞINDA çağrıldığını
	// bildirir; bu bir programlama hatasıdır, istemci girdisi değil.
	CodeTxRequired = "tax_tx_required"
)

// rollbackTimeout iptal edilmiş bir context üzerinde geri almaya tanınan
// süredir.
//
// Geri alma, çağıranın context'i sona ermiş olsa BİLE denenmelidir: aksi hâlde
// işlem, bağlantı havuza dönene kadar açık kalırdı.
const rollbackTimeout = 5 * time.Second

// txKeyType context anahtarının tipidir; dışarıdan üretilemesin diye dışa
// aktarılmaz.
type txKeyType struct{}

// txKey işlem tutamağının context'teki anahtarıdır.
var txKey = txKeyType{}

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

// WithTx fn'i tek bir veritabanı işleminde çalıştırır.
//
// fn'e verilen context işlemi TAŞIR: o context ile çağrılan her depo metodu
// aynı işlemde koşar. İşlem içindeki her çağrı bu yüzden FN'E VERİLEN
// context'le yapılmalıdır; dıştaki ctx kullanılırsa o çağrı işlemin dışına
// düşer ve atomiklik sessizce kaybedilir.
//
// fn hata döndürürse işlem GERİ ALINIR ve hata yukarı geçer.
//
// Atomiklik iki ayrı sınıf için zorunludur:
//
//   - Silme yolları: bir bölge silinirken önce bölge, sonra oranları, sonra
//     kuralları yumuşak silinir. Arada hata oluşursa bölge silinmiş ama
//     oranları canlı kalırdı; o oranlar hiçbir hesaba girmez ama aynı ülkeye
//     açılan yeni bir bölgenin yanında yetim satır olarak durur ve rapor
//     toplamlarını bozardı.
//   - Servis kuralının okuduğu satırla yazdığı satır: "bölge canlı mı"
//     denetimiyle yazma arasına giren bir silme, ikisi ayrı işlemdeyken
//     görülemez (bkz. [Repo.LockTaxRegion]).
//
// Çağrılar İÇ İÇE geçerse YENİ bir işlem AÇILMAZ, var olan kullanılır:
// PostgreSQL'de iç içe işlem bir savepoint demektir ve dıştaki işlemin
// atomikliği hakkında yanıltıcı bir güven verirdi.
func (r *Repo) WithTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if err := r.ready(); err != nil {
		return err
	}
	if _, ok := txFromContext(ctx); ok {
		return fn(ctx)
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return wrapDB(err, "işlem başlatılamadı")
	}

	committed := false
	defer func() {
		if committed {
			return
		}
		// Çağıranınkinden BAĞIMSIZ, kısa ömürlü bir context kullanılır:
		// çağıranın ctx'i iptal edilmişse onunla yapılan geri alma da anında
		// başarısız olur ve işlem açık kalırdı.
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
		defer cancel()
		_ = tx.Rollback(rollbackCtx)
	}()

	if err := fn(context.WithValue(ctx, txKey, tx)); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return wrapDB(err, "işlem tamamlanamadı")
	}
	committed = true
	return nil
}

// txFromContext context'teki işlem tutamağını döner.
func txFromContext(ctx context.Context) (pgx.Tx, bool) {
	tx, ok := ctx.Value(txKey).(pgx.Tx)
	return tx, ok
}

// queries context'e uyan sorgu kümesini döner: işlem varsa ona, yoksa havuza
// bağlı olanı.
func (r *Repo) queries(ctx context.Context) *taxdb.Queries {
	if tx, ok := txFromContext(ctx); ok {
		return r.q.WithTx(tx)
	}
	return r.q
}

// requireTx kilit alan metotların işlem içinde çağrıldığını doğrular.
func requireTx(ctx context.Context, op string) error {
	if _, ok := txFromContext(ctx); !ok {
		return errors.Internal(CodeTxRequired,
			"%s işlem içinde çağrılmalıdır; işlemsiz bir FOR SHARE kilidi hiçbir şeyi korumaz", op)
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
