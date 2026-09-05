package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
	"github.com/bdrtr/gobit/internal/modules/customer/repository/customerdb"
)

// Misafirden hesaba geçişin hata kodları.
const (
	// CodeAlreadyAccount kaydın zaten bir hesap olduğunu bildirir.
	CodeAlreadyAccount = "customer_already_account"
	// CodeEmailTaken e-postanın başka bir hesap tarafından kullanıldığını
	// bildirir.
	CodeEmailTaken = "customer_email_taken"
)

// CreateCustomer yeni bir müşteri yazar.
//
// Kayıtlı bir hesabın e-postası zaten kullanılıyorsa errors.Conflict döner;
// kural veritabanındaki kısmi benzersiz indekstedir (bkz. [IndexAccountEmail])
// ve uygulama tarafında tekrarlanmaz — tekrarlansaydı iki eşzamanlı kayıt
// arasındaki yarışı yine indeks çözerdi.
func (r *Repo) CreateCustomer(ctx context.Context, c models.Customer) (models.Customer, error) {
	if err := r.ready(); err != nil {
		return models.Customer{}, err
	}

	meta, err := fromMetadata(c.Metadata)
	if err != nil {
		return models.Customer{}, err
	}

	row, err := r.q.InsertCustomer(ctx, customerdb.InsertCustomerParams{
		ID:         c.ID,
		Email:      c.Email,
		FirstName:  c.FirstName,
		LastName:   c.LastName,
		Phone:      c.Phone,
		HasAccount: c.HasAccount,
		Metadata:   meta,
		CreatedAt:  fromTime(c.CreatedAt),
	})
	if err != nil {
		if ConstraintName(err) == IndexAccountEmail {
			return models.Customer{}, errors.Wrap(err, errors.KindConflict, CodeEmailTaken,
				"%q e-postasıyla kayıtlı bir hesap zaten var", c.Email)
		}
		return models.Customer{}, wrapDB(err, "müşteri oluşturulamadı")
	}
	return toCustomer(row)
}

// GetCustomer kimliğe göre müşteri döner; yoksa errors.NotFound.
func (r *Repo) GetCustomer(ctx context.Context, id string) (models.Customer, error) {
	if err := r.ready(); err != nil {
		return models.Customer{}, err
	}

	row, err := r.q.GetCustomer(ctx, id)
	if err != nil {
		return models.Customer{}, notFoundOr(err, CodeCustomerNotFound, "müşteri bulunamadı: %s", id)
	}
	return toCustomer(row)
}

// GetAccountByEmail e-postaya göre KAYITLI hesabı döner; yoksa errors.NotFound.
//
// Misafir kayıtları bilinçli olarak dışarıda bırakılır: aynı e-postayla birden
// çok misafir olabildiği için "e-postaya göre tek müşteri" sorusunun misafirler
// arasında tek bir doğru yanıtı yoktur (bkz. models.Customer).
func (r *Repo) GetAccountByEmail(ctx context.Context, email string) (models.Customer, error) {
	if err := r.ready(); err != nil {
		return models.Customer{}, err
	}

	row, err := r.q.GetAccountByEmail(ctx, email)
	if err != nil {
		return models.Customer{}, notFoundOr(err, CodeCustomerNotFound,
			"%q e-postasıyla kayıtlı hesap bulunamadı", email)
	}
	return toCustomer(row)
}

// ListCustomers süzgeçlenmiş ve sayfalanmış müşteri listesini, filtreye uyan
// TOPLAM kayıt sayısıyla birlikte döner.
func (r *Repo) ListCustomers(
	ctx context.Context,
	filter models.CustomerFilter,
	limit, offset int64,
) ([]models.Customer, int64, error) {
	if err := r.ready(); err != nil {
		return nil, 0, err
	}

	// The cursor arrives as SQL NULL when it names no position; the COALESCE
	// sentinels in the query turn that into "start at the top". Sending a zero
	// TIME instead would make the first page come back empty with no error
	// anywhere.
	afterAt := pgtype.Timestamptz{}
	if !filter.After.Time.IsZero() {
		afterAt = pgtype.Timestamptz{Time: filter.After.Time, Valid: true}
	}

	var afterID *string
	if filter.After.ID != "" {
		afterID = &filter.After.ID
	}

	rows, err := r.q.ListCustomers(ctx, customerdb.ListCustomersParams{
		Email:      filter.Email,
		HasAccount: filter.HasAccount,
		GroupID:    filter.GroupID,
		Lim:        toInt32(limit),
		Off:        toInt32(offset),
		AfterAt:    afterAt,
		AfterID:    afterID,
	})
	if err != nil {
		return nil, 0, wrapDB(err, "müşteri listesi alınamadı")
	}

	total, err := r.q.CountCustomers(ctx, customerdb.CountCustomersParams{
		Email:      filter.Email,
		HasAccount: filter.HasAccount,
		GroupID:    filter.GroupID,
	})
	if err != nil {
		return nil, 0, wrapDB(err, "müşteri sayısı alınamadı")
	}

	customers, err := toCustomers(rows)
	if err != nil {
		return nil, 0, err
	}
	return customers, total, nil
}

// GetCustomersByIDs verilen kimliklere karşılık gelen müşterileri TEK sorguda
// döner. Bulunamayan kimlik için kayıt dönmez; bu bir hata değildir
// (Query katmanının FetchByIDs sözleşmesi, ADR 0004).
func (r *Repo) GetCustomersByIDs(ctx context.Context, ids []string) ([]models.Customer, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []models.Customer{}, nil
	}

	rows, err := r.q.ListCustomersByIDs(ctx, ids)
	if err != nil {
		return nil, wrapDB(err, "müşteriler alınamadı")
	}
	return toCustomers(rows)
}

// UpdateCustomer müşterinin verilen alanlarını günceller; yoksa errors.NotFound.
func (r *Repo) UpdateCustomer(
	ctx context.Context,
	id string,
	patch models.CustomerPatch,
	now time.Time,
) (models.Customer, error) {
	if err := r.ready(); err != nil {
		return models.Customer{}, err
	}

	meta, err := patchMetadata(patch.Metadata)
	if err != nil {
		return models.Customer{}, err
	}

	row, err := r.q.UpdateCustomer(ctx, customerdb.UpdateCustomerParams{
		ID:        id,
		Email:     patch.Email,
		FirstName: patch.FirstName,
		LastName:  patch.LastName,
		Phone:     patch.Phone,
		Metadata:  meta,
		UpdatedAt: fromTime(now),
	})
	if err != nil {
		if ConstraintName(err) == IndexAccountEmail {
			return models.Customer{}, errors.Wrap(err, errors.KindConflict, CodeEmailTaken,
				"e-posta başka bir hesap tarafından kullanılıyor")
		}
		return models.Customer{}, notFoundOr(err, CodeCustomerNotFound, "müşteri bulunamadı: %s", id)
	}
	return toCustomer(row)
}

// PromoteGuest misafir kaydını hesaba çevirir.
//
// Üç durum ayrı ayrı raporlanır ve hepsi TEK işlemde, müşteri satırı KİLİTLİYKEN
// karara bağlanır:
//
//   - Kayıt yoksa errors.NotFound.
//   - Kayıt zaten hesapsa errors.Conflict ([CodeAlreadyAccount]).
//   - E-posta başka bir hesaba aitse errors.Conflict ([CodeEmailTaken]).
//
// Ön denetim kilit altında yapılsa bile kısmi benzersiz indeks SON kapı olarak
// kalır: denetimle güncelleme arasına giren başka bir işlem (aynı e-postayla
// yeni bir hesap açan) yalnızca indeksle yakalanır. Bu yüzden benzersizlik
// ihlali de aynı koda çevrilir; çağıran iki yolu ayırt etmek zorunda kalmaz.
func (r *Repo) PromoteGuest(ctx context.Context, id string, now time.Time) (models.Customer, error) {
	var out models.Customer

	err := r.inTx(ctx, func(q *customerdb.Queries) error {
		current, err := q.GetCustomerForUpdate(ctx, id)
		if err != nil {
			return notFoundOr(err, CodeCustomerNotFound, "müşteri bulunamadı: %s", id)
		}
		if current.HasAccount {
			return errors.Conflict(CodeAlreadyAccount, "müşteri zaten bir hesaba sahip: %s", id)
		}

		taken, err := q.AccountEmailTakenByOther(ctx, customerdb.AccountEmailTakenByOtherParams{
			Email: current.Email,
			ID:    id,
		})
		if err != nil {
			return wrapDB(err, "e-posta çakışması denetlenemedi")
		}
		if taken {
			return errors.Conflict(CodeEmailTaken,
				"%q e-postasıyla kayıtlı bir hesap zaten var", current.Email)
		}

		row, err := q.PromoteCustomerToAccount(ctx, customerdb.PromoteCustomerToAccountParams{
			ID:        id,
			UpdatedAt: fromTime(now),
		})
		if err != nil {
			if ConstraintName(err) == IndexAccountEmail {
				return errors.Wrap(err, errors.KindConflict, CodeEmailTaken,
					"%q e-postasıyla kayıtlı bir hesap zaten var", current.Email)
			}
			return notFoundOr(err, CodeCustomerNotFound, "müşteri hesaba çevrilemedi: %s", id)
		}

		out, err = toCustomer(row)
		return err
	})
	if err != nil {
		return models.Customer{}, err
	}
	return out, nil
}

// DeleteCustomer müşteriyi ve adreslerini soft delete ile siler; yoksa
// errors.NotFound.
//
// Adresler AYNI işlemde silinir: foreign key'in ON DELETE CASCADE'i yalnızca
// gerçek silmede çalışır ve yumuşak silme bir UPDATE olduğu için adresleri
// kendiliğinden götürmez. Grup üyelikleri ise BIRAKILIR — silinmiş müşteri
// zaten hiçbir listede görünmez ve üyelik satırı, kayıt bir gün gerçekten
// silindiğinde cascade ile gider.
func (r *Repo) DeleteCustomer(ctx context.Context, id string, now time.Time) error {
	return r.inTx(ctx, func(q *customerdb.Queries) error {
		if _, err := q.SoftDeleteCustomer(ctx, customerdb.SoftDeleteCustomerParams{
			ID:        id,
			DeletedAt: fromTime(now),
		}); err != nil {
			return notFoundOr(err, CodeCustomerNotFound, "müşteri bulunamadı: %s", id)
		}

		if err := q.SoftDeleteAddressesOfCustomer(ctx, customerdb.SoftDeleteAddressesOfCustomerParams{
			CustomerID: id,
			DeletedAt:  fromTime(now),
		}); err != nil {
			return wrapDB(err, "müşterinin adresleri silinemedi: %s", id)
		}
		return nil
	})
}

// toCustomer üretilen satırı domain modeline çevirir.
func toCustomer(row customerdb.Customer) (models.Customer, error) {
	meta, err := toMetadata(row.Metadata)
	if err != nil {
		return models.Customer{}, err
	}
	return models.Customer{
		ID:         row.ID,
		Email:      row.Email,
		FirstName:  row.FirstName,
		LastName:   row.LastName,
		Phone:      row.Phone,
		HasAccount: row.HasAccount,
		Metadata:   meta,
		CreatedAt:  toTime(row.CreatedAt),
		UpdatedAt:  toTime(row.UpdatedAt),
		DeletedAt:  toTimePtr(row.DeletedAt),
	}, nil
}

// toCustomers satır dilimini domain modellerine çevirir.
func toCustomers(rows []customerdb.Customer) ([]models.Customer, error) {
	out := make([]models.Customer, 0, len(rows))
	for i := range rows {
		c, err := toCustomer(rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}
