// Package repository file modülünün veritabanı erişimidir.
//
// SADECE bu modülün tablosuna dokunur (plan Bölüm 4). sqlc üretimi kod
// repository/filedb altındadır ve elle düzenlenmez; bu paket onun üstüne iki
// şey ekler:
//
//   - Çeviri: pgtype ve üretilmiş satır tipleri BU PAKETİN DIŞINA ÇIKMAZ,
//     models tiplerine çevrilir.
//   - Sınıflandırma: sürücü hataları core/errors tipli hatalarına çevrilir;
//     satır bulunamaması NotFound, benzersizlik ihlali Conflict olur
//     (plan Bölüm 2.7 — status kodunu handler seçmez).
//
// # İŞLEM (transaction) YOKTUR ve gerekmez
//
// Diğer modüllerin deposu WithTx taşır; burada yoktur. Bir yüklemenin iki
// tarafı vardır — DEPODAKİ dosya ve DEFTERDEKİ satır — ve ikisi ayrı
// sistemlerde durur; veritabanı işlemi dosyayı geri alamaz. Tutarlılık bu
// yüzden işlemle değil SIRAYLA sağlanır: yazarken önce dosya sonra satır,
// silerken önce dosya sonra satır. Sıranın gerekçesi ilgili çağrıların
// yanındadır (bkz. service paketi).
package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/file/models"
	"github.com/bdrtr/gobit/internal/modules/file/repository/filedb"
)

// Hata kodları; çağıran taraf errors.CodeOf ile bunlara bakabilir.
const (
	// CodeUploadNotFound istenen yükleme kaydının bulunamadığını bildirir.
	CodeUploadNotFound = "file_upload_not_found"
	// CodeUploadExists aynı depo anahtarıyla ikinci bir kayıt açılmak
	// istendiğini bildirir.
	CodeUploadExists = "file_upload_already_exists"
	// CodeConstraintViolation veritabanı kısıtının ihlal edildiğini bildirir.
	CodeConstraintViolation = "file_constraint_violation"
	// CodeQueryFailed beklenmeyen bir veritabanı hatasını bildirir.
	CodeQueryFailed = "file_query_failed"
	// CodeCanceled bağlam iptalini bildirir.
	CodeCanceled = "file_canceled"
	// CodeNotReady deponun havuz olmadan kurulduğunu bildirir.
	CodeNotReady = "file_repository_not_ready"
)

// PostgreSQL SQLSTATE kodları (ihtiyaç duyulanlar).
const (
	sqlstateUniqueViolation      = "23505"
	sqlstateCheckViolation       = "23514"
	sqlstateNotNullViolation     = "23502"
	sqlstateStringDataRightTrunc = "22001"
)

// constraintStorageKeyUniq depo anahtarını benzersiz kılan indeksin adıdır;
// migration'daki adla BİREBİR aynıdır.
const constraintStorageKeyUniq = "file_uploads_storage_key_uniq"

// Repo yükleme defterine erişimi sağlar. Eşzamanlı kullanıma güvenlidir.
type Repo struct {
	q *filedb.Queries
}

// New verilen havuz üzerinde çalışan bir depo üretir.
//
// pool nil ise bu, kurulumda değil ilk çağrıda tipli bir hata olarak
// bildirilir; kurulum yolu panik üretmez.
func New(pool *pgxpool.Pool) *Repo {
	if pool == nil {
		return &Repo{}
	}

	return &Repo{q: filedb.New(pool)}
}

// ready havuzun kullanılabilir olduğunu doğrular.
func (r *Repo) ready() error {
	if r == nil || r.q == nil {
		return errors.Unavailable(CodeNotReady, "file veritabanı havuzu kurulmamış")
	}

	return nil
}

// CreateUpload yükleme kaydını yazar.
//
// Aynı depo anahtarıyla ikinci bir kayıt errors.Conflict döner. Normal akışta
// oluşamaz — anahtarı sağlayıcı üretir ve rastgeleliği ULID'inkiyle aynıdır —
// ama eşlenmesi gerekir: eşlenmeseydi, anahtar üretimi bir gün bozulduğunda
// arıza "sunucu hatası" olarak görünür ve sebebi kaybolurdu.
func (r *Repo) CreateUpload(ctx context.Context, u models.Upload) (models.Upload, error) {
	if err := r.ready(); err != nil {
		return models.Upload{}, err
	}

	row, err := r.q.CreateFileUpload(ctx, filedb.CreateFileUploadParams{
		ID:           u.ID,
		StorageKey:   u.StorageKey,
		ProviderID:   u.ProviderID,
		ContentType:  u.ContentType,
		Size:         u.Size,
		Checksum:     u.Checksum,
		OriginalName: u.OriginalName,
		Url:          u.URL,
		UploadedBy:   u.UploadedBy,
	})
	if err != nil {
		return models.Upload{}, classify(err, "yükleme kaydı yazılamadı: %s", u.StorageKey)
	}

	return toUpload(row), nil
}

// GetUpload kaydı kimliğiyle döner; yoksa NotFound.
func (r *Repo) GetUpload(ctx context.Context, id string) (models.Upload, error) {
	if err := r.ready(); err != nil {
		return models.Upload{}, err
	}

	row, err := r.q.GetFileUpload(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Upload{}, uploadNotFound("kimlik", id)
		}

		return models.Upload{}, classify(err, "yükleme kaydı okunamadı: %s", id)
	}

	return toUpload(row), nil
}

// GetUploadByKey kaydı DEPO ANAHTARIYLA döner; yoksa NotFound.
//
// Sunum yolunun tek sorgusudur: adres çubuğundan gelen anahtar önce buraya
// sorulur ve satır yoksa dosya sistemine hiç dokunulmaz.
func (r *Repo) GetUploadByKey(ctx context.Context, key string) (models.Upload, error) {
	if err := r.ready(); err != nil {
		return models.Upload{}, err
	}

	row, err := r.q.GetFileUploadByKey(ctx, key)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Upload{}, uploadNotFound("anahtar", key)
		}

		return models.Upload{}, classify(err, "yükleme kaydı okunamadı")
	}

	return toUpload(row), nil
}

// ListUploads kayıtları sayfalayarak döner.
// İkinci dönüş değeri TÜM satırların sayısıdır.
func (r *Repo) ListUploads(ctx context.Context, filter models.UploadFilter) ([]models.Upload, int64, error) {
	if err := r.ready(); err != nil {
		return nil, 0, err
	}

	rows, err := r.q.ListFileUploads(ctx, filedb.ListFileUploadsParams{
		RowLimit:  filter.Limit,
		RowOffset: filter.Offset,
	})
	if err != nil {
		return nil, 0, classify(err, "yükleme listesi alınamadı")
	}

	total, err := r.q.CountFileUploads(ctx)
	if err != nil {
		return nil, 0, classify(err, "yüklemeler sayılamadı")
	}

	out := make([]models.Upload, 0, len(rows))
	// Dilim İNDEKSLE gezilir: değerle gezmek her yinelemede satır yapısının
	// tamamını kopyalardı.
	for i := range rows {
		out = append(out, toUpload(rows[i]))
	}

	return out, total, nil
}

// DeleteUpload kaydı siler. İkinci dönüş değeri satırın GERÇEKTEN silinip
// silinmediğidir.
//
// Olmayan bir kimlik hata DEĞİLDİR: silme bir son durum iddiasıdır ve çağıran
// "zaten yoktu" ile "şimdi sildim"i ayırt edebilmelidir — ama ikisi de
// başarıdır.
func (r *Repo) DeleteUpload(ctx context.Context, id string) (bool, error) {
	if err := r.ready(); err != nil {
		return false, err
	}

	silinen, err := r.q.DeleteFileUpload(ctx, id)
	if err != nil {
		return false, classify(err, "yükleme kaydı silinemedi: %s", id)
	}

	return silinen > 0, nil
}

// toUpload üretilen satırı domain modeline çevirir.
func toUpload(row filedb.FileUpload) models.Upload {
	return models.Upload{
		ID:           row.ID,
		StorageKey:   row.StorageKey,
		ProviderID:   row.ProviderID,
		ContentType:  row.ContentType,
		Size:         row.Size,
		Checksum:     row.Checksum,
		OriginalName: row.OriginalName,
		URL:          row.Url,
		UploadedBy:   row.UploadedBy,
		CreatedAt:    toTime(row.CreatedAt),
		UpdatedAt:    toTime(row.UpdatedAt),
	}
}

// toTime NOT NULL bir zaman damgasını UTC time.Time'a çevirir.
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

// uploadNotFound bulunamayan kayıt için tipli hata üretir.
//
// Aranan ALANIN adı mesaja girer: aynı hata hem kimlikle hem depo anahtarıyla
// yapılan aramadan gelebilir ve ikisinin düzeltmesi farklıdır.
func uploadNotFound(alan, deger string) error {
	return errors.NotFound(CodeUploadNotFound, "yükleme bulunamadı (%s: %s)", alan, deger)
}

// sprintf hata mesajını bir kez biçimlendirir.
//
// Argümansız çağrılarda format DEĞİŞTİRİLMEDEN döner; aksi hâlde mesajdaki bir
// yüzde işareti (örn. "%!d(MISSING)") teşhis metnini bozardı.
func sprintf(format string, a ...any) string {
	if len(a) == 0 {
		return format
	}

	return fmt.Sprintf(format, a...)
}

// classify ham bir veritabanı hatasını tipli hataya çevirir.
//
// Sınıflandırma bilinçlidir: benzersizlik ihlali ÇAKIŞMADIR (409), kısıt
// ihlali istemci hatasıdır (422), iptal geçici erişilemezliktir (503); geri
// kalan her şey sunucu hatasıdır ve mesajı istemciye SIZDIRILMAZ (bkz.
// core/http).
func classify(err error, format string, a ...any) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return errors.Wrap(err, errors.KindUnavailable, CodeCanceled, format, a...)
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case sqlstateUniqueViolation:
			if pgErr.ConstraintName == constraintStorageKeyUniq {
				return errors.Wrap(err, errors.KindConflict, CodeUploadExists,
					"bu depo anahtarı için zaten bir yükleme kaydı var")
			}
		case sqlstateCheckViolation, sqlstateNotNullViolation, sqlstateStringDataRightTrunc:
			return errors.Wrap(err, errors.KindInvalid, CodeConstraintViolation,
				"%s (kısıt: %s)", sprintf(format, a...), pgErr.ConstraintName)
		}
	}

	return errors.Wrap(err, errors.KindInternal, CodeQueryFailed, format, a...)
}
