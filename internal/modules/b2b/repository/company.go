package repository

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/internal/modules/b2b/models"
	"github.com/bdrtr/gobit/internal/modules/b2b/repository/b2bdb"
)

// CreateCompany yeni bir şirket yazar.
func (r *Repo) CreateCompany(ctx context.Context, c models.Company) (models.Company, error) {
	if err := r.ready(); err != nil {
		return models.Company{}, err
	}

	row, err := r.q.InsertCompany(ctx, b2bdb.InsertCompanyParams{
		ID:                       c.ID,
		Name:                     c.Name,
		Email:                    c.Email,
		Phone:                    c.Phone,
		Address:                  c.Address,
		City:                     c.City,
		PostalCode:               c.PostalCode,
		CountryCode:              c.CountryCode,
		CurrencyCode:             c.CurrencyCode,
		SpendingLimitResetPeriod: string(c.SpendingLimitResetPeriod),
		CreatedAt:                fromTime(c.CreatedAt),
	})
	if err != nil {
		return models.Company{}, wrapDB(err, "şirket oluşturulamadı")
	}
	return toCompany(row), nil
}

// GetCompany kimliğe göre şirket döner; yoksa errors.NotFound.
func (r *Repo) GetCompany(ctx context.Context, id string) (models.Company, error) {
	if err := r.ready(); err != nil {
		return models.Company{}, err
	}

	row, err := r.q.GetCompany(ctx, id)
	if err != nil {
		return models.Company{}, notFoundOr(err, CodeCompanyNotFound, "şirket bulunamadı: %s", id)
	}
	return toCompany(row), nil
}

// ListCompanies sayfalanmış şirket listesini ve TOPLAM kayıt sayısını döner.
func (r *Repo) ListCompanies(
	ctx context.Context,
	filter models.CompanyFilter,
	limit, offset int64,
) ([]models.Company, int64, error) {
	if err := r.ready(); err != nil {
		return nil, 0, err
	}

	rows, err := r.q.ListCompanies(ctx, b2bdb.ListCompaniesParams{
		Email: filter.Email,
		Lim:   toInt32(limit),
		Off:   toInt32(offset),
	})
	if err != nil {
		return nil, 0, wrapDB(err, "şirket listesi alınamadı")
	}

	total, err := r.q.CountCompanies(ctx, filter.Email)
	if err != nil {
		return nil, 0, wrapDB(err, "şirket sayısı alınamadı")
	}
	return toCompanies(rows), total, nil
}

// UpdateCompany şirketin verilen alanlarını günceller; yoksa errors.NotFound.
func (r *Repo) UpdateCompany(
	ctx context.Context,
	id string,
	patch models.CompanyPatch,
	now time.Time,
) (models.Company, error) {
	if err := r.ready(); err != nil {
		return models.Company{}, err
	}

	var resetPeriod *string
	if patch.SpendingLimitResetPeriod != nil {
		value := string(*patch.SpendingLimitResetPeriod)
		resetPeriod = &value
	}

	row, err := r.q.UpdateCompany(ctx, b2bdb.UpdateCompanyParams{
		ID:           id,
		Name:         patch.Name,
		Email:        patch.Email,
		Phone:        patch.Phone,
		Address:      patch.Address,
		City:         patch.City,
		PostalCode:   patch.PostalCode,
		CountryCode:  patch.CountryCode,
		CurrencyCode: patch.CurrencyCode,
		ResetPeriod:  resetPeriod,
		UpdatedAt:    fromTime(now),
	})
	if err != nil {
		return models.Company{}, notFoundOr(err, CodeCompanyNotFound, "şirket bulunamadı: %s", id)
	}
	return toCompany(row), nil
}

// DeleteCompany şirketi ve ÇALIŞANLARINI yumuşak siler; şirket yoksa
// errors.NotFound. Dönen dilim, silinen çalışanların kimlikleridir.
//
// # Neden çalışanlar da siliniyor
//
// Sarkan bir çalışan kaydının vitrinde sahibi olmazdı: "kendi şirketim" sorusu,
// artık okunamayan (yumuşak silinmiş) bir şirkete çözülür ve müşteri ya 404
// alır ya da silinmiş bir şirketi görürdü. İkincisi daha kötüdür — kayıt,
// hâlâ bir harcama limiti taşır ve o limitin arkasında ödeme yapacak bir tüzel
// kişi yoktur. Bu yüzden değişmez şudur: CANLI bir çalışan kaydı DAİMA canlı
// bir şirkete aittir.
//
// İkisi TEK işlemde yapılır; arada kalan bir hata tam da yasaklanan durumu
// üretirdi. Çalışanların müşteri BAĞLARI burada değil, servis katmanında
// kaldırılır (link ayrı bir alt sistemdir ve bu işleme katılamaz); kimliklerin
// dönmesinin sebebi de budur.
func (r *Repo) DeleteCompany(ctx context.Context, id string, now time.Time) ([]string, error) {
	var employeeIDs []string

	err := r.inTx(ctx, func(q *b2bdb.Queries) error {
		if _, err := q.SoftDeleteCompany(ctx, b2bdb.SoftDeleteCompanyParams{
			ID:        id,
			DeletedAt: fromTime(now),
		}); err != nil {
			return notFoundOr(err, CodeCompanyNotFound, "şirket bulunamadı: %s", id)
		}

		ids, err := q.SoftDeleteEmployeesOfCompany(ctx, b2bdb.SoftDeleteEmployeesOfCompanyParams{
			CompanyID: id,
			DeletedAt: fromTime(now),
		})
		if err != nil {
			return wrapDB(err, "şirketin çalışanları silinemedi: %s", id)
		}
		employeeIDs = ids
		return nil
	})
	if err != nil {
		return nil, err
	}
	return employeeIDs, nil
}

// toCompany üretilen satırı domain modeline çevirir.
func toCompany(row b2bdb.B2bCompany) models.Company {
	return models.Company{
		ID:                       row.ID,
		Name:                     row.Name,
		Email:                    row.Email,
		Phone:                    row.Phone,
		Address:                  row.Address,
		City:                     row.City,
		PostalCode:               row.PostalCode,
		CountryCode:              row.CountryCode,
		CurrencyCode:             row.CurrencyCode,
		SpendingLimitResetPeriod: models.SpendingResetPeriod(row.SpendingLimitResetPeriod),
		CreatedAt:                toTime(row.CreatedAt),
		UpdatedAt:                toTime(row.UpdatedAt),
		DeletedAt:                toTimePtr(row.DeletedAt),
	}
}

// toCompanies satır dilimini domain modellerine çevirir.
func toCompanies(rows []b2bdb.B2bCompany) []models.Company {
	out := make([]models.Company, 0, len(rows))
	for i := range rows {
		out = append(out, toCompany(rows[i]))
	}
	return out
}
