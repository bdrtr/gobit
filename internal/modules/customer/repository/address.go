package repository

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
	"github.com/bdrtr/gobit/internal/modules/customer/repository/customerdb"
)

// CodeInvalidDefaultKind tanımsız bir varsayılan işaret türünü bildirir.
const CodeInvalidDefaultKind = "customer_invalid_default_kind"

// CreateAddress müşterinin yeni adresini yazar.
//
// Adresin kendisi varsayılan olarak işaretlenecekse önce müşterinin O TÜRDEKİ eski
// varsayılanı temizlenir; ikisi tek işlemde yapılır. Kilit sırası HER akışta
// aynıdır — önce müşteri satırı, sonra adresler (bkz. [Repo.SetDefaultAddress]).
//
// Müşterinin varlığı kilitle birlikte doğrulanır: yalnızca foreign key'e
// güvenilseydi eksik müşteri istemciye 404 yerine 422 olarak dönerdi.
func (r *Repo) CreateAddress(ctx context.Context, a models.CustomerAddress) (models.CustomerAddress, error) {
	var out models.CustomerAddress

	err := r.inTx(ctx, func(q *customerdb.Queries) error {
		if _, err := q.GetCustomerForUpdate(ctx, a.CustomerID); err != nil {
			return notFoundOr(err, CodeCustomerNotFound, "müşteri bulunamadı: %s", a.CustomerID)
		}

		if err := clearDefaults(ctx, q, a.CustomerID, a.IsDefaultShipping, a.IsDefaultBilling, a.CreatedAt); err != nil {
			return err
		}

		row, err := q.InsertCustomerAddress(ctx, customerdb.InsertCustomerAddressParams{
			ID:                a.ID,
			CustomerID:        a.CustomerID,
			FirstName:         a.FirstName,
			LastName:          a.LastName,
			Company:           a.Company,
			Address1:          a.Address1,
			Address2:          a.Address2,
			City:              a.City,
			CountryCode:       a.CountryCode,
			PostalCode:        a.PostalCode,
			Phone:             a.Phone,
			IsDefaultShipping: a.IsDefaultShipping,
			IsDefaultBilling:  a.IsDefaultBilling,
			CreatedAt:         fromTime(a.CreatedAt),
		})
		if err != nil {
			return wrapDB(err, "müşteri adresi oluşturulamadı")
		}
		out = toAddress(row)
		return nil
	})
	if err != nil {
		return models.CustomerAddress{}, err
	}
	return out, nil
}

// GetAddress adresi kimliği ve SAHİBİYLE birlikte döner; yoksa errors.NotFound.
func (r *Repo) GetAddress(ctx context.Context, customerID, addressID string) (models.CustomerAddress, error) {
	if err := r.ready(); err != nil {
		return models.CustomerAddress{}, err
	}

	row, err := r.q.GetCustomerAddress(ctx, customerdb.GetCustomerAddressParams{
		ID:         addressID,
		CustomerID: customerID,
	})
	if err != nil {
		return models.CustomerAddress{}, notFoundOr(err, CodeAddressNotFound,
			"müşteri adresi bulunamadı: %s", addressID)
	}
	return toAddress(row), nil
}

// ListAddresses müşterinin adreslerini döner.
func (r *Repo) ListAddresses(ctx context.Context, customerID string) ([]models.CustomerAddress, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}

	rows, err := r.q.ListCustomerAddresses(ctx, customerID)
	if err != nil {
		return nil, wrapDB(err, "müşterinin adresleri alınamadı: %s", customerID)
	}

	out := make([]models.CustomerAddress, 0, len(rows))
	for i := range rows {
		out = append(out, toAddress(rows[i]))
	}
	return out, nil
}

// UpdateAddress adresin verilen alanlarını günceller; yoksa errors.NotFound.
//
// Varsayılan işaretleri burada DEĞİŞTİRİLEMEZ: işaret, müşterinin diğer
// adreslerini de ilgilendirdiği için tek satırlık bir güncellemeyle
// yapılamaz (bkz. [Repo.SetDefaultAddress]).
func (r *Repo) UpdateAddress(
	ctx context.Context,
	customerID, addressID string,
	patch models.AddressPatch,
	now time.Time,
) (models.CustomerAddress, error) {
	if err := r.ready(); err != nil {
		return models.CustomerAddress{}, err
	}

	row, err := r.q.UpdateCustomerAddress(ctx, customerdb.UpdateCustomerAddressParams{
		ID:          addressID,
		CustomerID:  customerID,
		FirstName:   patch.FirstName,
		LastName:    patch.LastName,
		Company:     patch.Company,
		Address1:    patch.Address1,
		Address2:    patch.Address2,
		City:        patch.City,
		CountryCode: patch.CountryCode,
		PostalCode:  patch.PostalCode,
		Phone:       patch.Phone,
		UpdatedAt:   fromTime(now),
	})
	if err != nil {
		return models.CustomerAddress{}, notFoundOr(err, CodeAddressNotFound,
			"müşteri adresi bulunamadı: %s", addressID)
	}
	return toAddress(row), nil
}

// DeleteAddress adresi soft delete ile siler; yoksa errors.NotFound.
//
// Adresin taşıdığı varsayılan işareti için ayrıca temizleme gerekmez: kısmi
// benzersiz indeksler deleted_at IS NULL koşuluyla tanımlıdır, silinen satır
// indeksin kapsamından çıkar ve müşteri yeni bir varsayılan atayabilir.
func (r *Repo) DeleteAddress(ctx context.Context, customerID, addressID string, now time.Time) error {
	if err := r.ready(); err != nil {
		return err
	}

	if _, err := r.q.SoftDeleteCustomerAddress(ctx, customerdb.SoftDeleteCustomerAddressParams{
		ID:         addressID,
		CustomerID: customerID,
		DeletedAt:  fromTime(now),
	}); err != nil {
		return notFoundOr(err, CodeAddressNotFound, "müşteri adresi bulunamadı: %s", addressID)
	}
	return nil
}

// SetDefaultAddress adresi müşterinin varsayılan kargo ya da fatura adresi
// yapar.
//
// # Kilit sırası
//
// İşlem HER ZAMAN önce müşteri satırını kilitler, sonra adreslerin satırlarına
// dokunur. Sıra sabit olduğu için aynı müşteriye gelen iki eşzamanlı atama
// birbirini bekler ve sırayla çalışır; ters sırada kilit alan bir akış olsaydı
// veritabanı işlemlerden birini kilitlenme (deadlock) ile öldürürdü.
//
// # Neden temizleme yetmez
//
// "Eskisini temizle, yenisini işaretle" adımı tek başına doğruluğun kaynağı
// DEĞİLDİR; kısıt, müşteri başına tek işaretli satıra izin veren kısmi
// benzersiz indekstir. Temizleme adımı yalnızca o kısıtı sağlamanın yoludur.
// Kilit ile indeks birlikte çalışır: kilit yarışı seri hâle getirir, indeks
// kilidin atlandığı ya da yanlış kurulduğu bir yolda ikinci işareti reddeder.
func (r *Repo) SetDefaultAddress(
	ctx context.Context,
	customerID, addressID string,
	kind models.DefaultKind,
	now time.Time,
) (models.CustomerAddress, error) {
	if !kind.Valid() {
		return models.CustomerAddress{}, errors.Invalid(CodeInvalidDefaultKind,
			"tanımsız varsayılan işaret türü: %d", uint8(kind))
	}

	var out models.CustomerAddress

	err := r.inTx(ctx, func(q *customerdb.Queries) error {
		if _, err := q.GetCustomerForUpdate(ctx, customerID); err != nil {
			return notFoundOr(err, CodeCustomerNotFound, "müşteri bulunamadı: %s", customerID)
		}
		// Adresin sahipliği ve canlılığı işaretlemeden ÖNCE doğrulanır; aksi
		// hâlde eski varsayılan temizlenir, yenisi hiç işaretlenemez ve müşteri
		// varsayılansız kalırdı.
		if _, err := q.GetCustomerAddress(ctx, customerdb.GetCustomerAddressParams{
			ID:         addressID,
			CustomerID: customerID,
		}); err != nil {
			return notFoundOr(err, CodeAddressNotFound, "müşteri adresi bulunamadı: %s", addressID)
		}

		if err := clearDefaults(ctx, q, customerID,
			kind == models.DefaultShipping, kind == models.DefaultBilling, now); err != nil {
			return err
		}

		var (
			row customerdb.CustomerAddress
			err error
		)
		if kind == models.DefaultShipping {
			row, err = q.MarkDefaultShipping(ctx, customerdb.MarkDefaultShippingParams{
				ID: addressID, CustomerID: customerID, UpdatedAt: fromTime(now),
			})
		} else {
			row, err = q.MarkDefaultBilling(ctx, customerdb.MarkDefaultBillingParams{
				ID: addressID, CustomerID: customerID, UpdatedAt: fromTime(now),
			})
		}
		if err != nil {
			return notFoundOr(err, CodeAddressNotFound, "müşteri adresi varsayılan yapılamadı: %s", addressID)
		}

		out = toAddress(row)
		return nil
	})
	if err != nil {
		return models.CustomerAddress{}, err
	}
	return out, nil
}

// clearDefaults istenen türlerdeki varsayılan işaretlerini kaldırır.
//
// Çağıran işlem İÇİNDEDİR ve müşteri satırını çoktan kilitlemiştir; bu yüzden
// burada yeniden kilit alınmaz.
func clearDefaults(
	ctx context.Context,
	q *customerdb.Queries,
	customerID string,
	shipping, billing bool,
	now time.Time,
) error {
	if shipping {
		if err := q.ClearDefaultShipping(ctx, customerdb.ClearDefaultShippingParams{
			CustomerID: customerID, UpdatedAt: fromTime(now),
		}); err != nil {
			return wrapDB(err, "varsayılan kargo adresi temizlenemedi: %s", customerID)
		}
	}
	if billing {
		if err := q.ClearDefaultBilling(ctx, customerdb.ClearDefaultBillingParams{
			CustomerID: customerID, UpdatedAt: fromTime(now),
		}); err != nil {
			return wrapDB(err, "varsayılan fatura adresi temizlenemedi: %s", customerID)
		}
	}
	return nil
}

// toAddress üretilen satırı domain modeline çevirir.
func toAddress(row customerdb.CustomerAddress) models.CustomerAddress {
	return models.CustomerAddress{
		ID:                row.ID,
		CustomerID:        row.CustomerID,
		FirstName:         row.FirstName,
		LastName:          row.LastName,
		Company:           row.Company,
		Address1:          row.Address1,
		Address2:          row.Address2,
		City:              row.City,
		CountryCode:       row.CountryCode,
		PostalCode:        row.PostalCode,
		Phone:             row.Phone,
		IsDefaultShipping: row.IsDefaultShipping,
		IsDefaultBilling:  row.IsDefaultBilling,
		CreatedAt:         toTime(row.CreatedAt),
		UpdatedAt:         toTime(row.UpdatedAt),
		DeletedAt:         toTimePtr(row.DeletedAt),
	}
}
