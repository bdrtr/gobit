package repository

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/internal/modules/b2b/models"
	"github.com/bdrtr/gobit/internal/modules/b2b/repository/b2bdb"
)

// CreateEmployee yeni bir çalışan yazar.
//
// [models.CompanyEmployee.CustomerID] YOK SAYILIR: müşteri bağı şemada değil
// link tablosundadır ve onu kuran servis katmanıdır. Şirket yoksa foreign key
// ihlali errors.Invalid'e çevrilir; çağıran şirketin varlığını önceden
// doğrulayarak daha iyi bir hata üretebilir.
func (r *Repo) CreateEmployee(ctx context.Context, e models.CompanyEmployee) (models.CompanyEmployee, error) {
	if err := r.ready(); err != nil {
		return models.CompanyEmployee{}, err
	}

	row, err := r.q.InsertEmployee(ctx, b2bdb.InsertEmployeeParams{
		ID:             e.ID,
		CompanyID:      e.CompanyID,
		SpendingLimit:  e.SpendingLimit,
		IsCompanyAdmin: e.IsCompanyAdmin,
		CreatedAt:      fromTime(e.CreatedAt),
	})
	if err != nil {
		return models.CompanyEmployee{}, wrapDB(err, "çalışan oluşturulamadı")
	}
	return toEmployee(row), nil
}

// GetEmployee kimliğe göre çalışan döner; yoksa errors.NotFound.
func (r *Repo) GetEmployee(ctx context.Context, id string) (models.CompanyEmployee, error) {
	if err := r.ready(); err != nil {
		return models.CompanyEmployee{}, err
	}

	row, err := r.q.GetEmployee(ctx, id)
	if err != nil {
		return models.CompanyEmployee{}, notFoundOr(err, CodeEmployeeNotFound, "çalışan bulunamadı: %s", id)
	}
	return toEmployee(row), nil
}

// ListEmployees sayfalanmış çalışan listesini ve TOPLAM kayıt sayısını döner.
func (r *Repo) ListEmployees(
	ctx context.Context,
	filter models.EmployeeFilter,
	limit, offset int64,
) ([]models.CompanyEmployee, int64, error) {
	if err := r.ready(); err != nil {
		return nil, 0, err
	}

	rows, err := r.q.ListEmployees(ctx, b2bdb.ListEmployeesParams{
		CompanyID:      filter.CompanyID,
		IsCompanyAdmin: filter.IsCompanyAdmin,
		Lim:            toInt32(limit),
		Off:            toInt32(offset),
	})
	if err != nil {
		return nil, 0, wrapDB(err, "çalışan listesi alınamadı")
	}

	total, err := r.q.CountEmployees(ctx, b2bdb.CountEmployeesParams{
		CompanyID:      filter.CompanyID,
		IsCompanyAdmin: filter.IsCompanyAdmin,
	})
	if err != nil {
		return nil, 0, wrapDB(err, "çalışan sayısı alınamadı")
	}
	return toEmployees(rows), total, nil
}

// UpdateEmployee çalışanın verilen alanlarını günceller; yoksa errors.NotFound.
func (r *Repo) UpdateEmployee(
	ctx context.Context,
	id string,
	patch models.EmployeePatch,
	now time.Time,
) (models.CompanyEmployee, error) {
	if err := r.ready(); err != nil {
		return models.CompanyEmployee{}, err
	}

	row, err := r.q.UpdateEmployee(ctx, b2bdb.UpdateEmployeeParams{
		ID:             id,
		ClearLimit:     patch.ClearSpendingLimit,
		SpendingLimit:  patch.SpendingLimit,
		IsCompanyAdmin: patch.IsCompanyAdmin,
		UpdatedAt:      fromTime(now),
	})
	if err != nil {
		return models.CompanyEmployee{}, notFoundOr(err, CodeEmployeeNotFound,
			"çalışan bulunamadı: %s", id)
	}
	return toEmployee(row), nil
}

// DeleteEmployee çalışanı yumuşak siler; yoksa errors.NotFound.
//
// Müşteri BAĞI burada kaldırılmaz; onu kaldıran servis katmanıdır (link ayrı
// bir alt sistemdir). Bağın kaldırılması şarttır: kalırsa müşteri, bağın tekil
// olması yüzünden bir daha hiçbir şirkete çalışan olarak eklenemez.
func (r *Repo) DeleteEmployee(ctx context.Context, id string, now time.Time) error {
	if err := r.ready(); err != nil {
		return err
	}

	if _, err := r.q.SoftDeleteEmployee(ctx, b2bdb.SoftDeleteEmployeeParams{
		ID:        id,
		DeletedAt: fromTime(now),
	}); err != nil {
		return notFoundOr(err, CodeEmployeeNotFound, "çalışan bulunamadı: %s", id)
	}
	return nil
}

// toEmployee üretilen satırı domain modeline çevirir.
//
// CustomerID BOŞ kalır: sütunu yoktur, değeri link'ten gelir ve servis
// katmanı doldurur (bkz. paket belgesi).
func toEmployee(row b2bdb.B2bCompanyEmployee) models.CompanyEmployee {
	return models.CompanyEmployee{
		ID:             row.ID,
		CompanyID:      row.CompanyID,
		SpendingLimit:  row.SpendingLimit,
		IsCompanyAdmin: row.IsCompanyAdmin,
		CreatedAt:      toTime(row.CreatedAt),
		UpdatedAt:      toTime(row.UpdatedAt),
		DeletedAt:      toTimePtr(row.DeletedAt),
	}
}

// toEmployees satır dilimini domain modellerine çevirir.
func toEmployees(rows []b2bdb.B2bCompanyEmployee) []models.CompanyEmployee {
	out := make([]models.CompanyEmployee, 0, len(rows))
	for i := range rows {
		out = append(out, toEmployee(rows[i]))
	}
	return out
}
