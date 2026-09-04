package repository

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
	"github.com/bdrtr/gobit/internal/modules/customer/repository/customerdb"
)

// CodeGroupNameTaken grup adının zaten kullanıldığını bildirir.
const CodeGroupNameTaken = "customer_group_name_taken"

// CreateGroup yeni bir müşteri grubu yazar.
func (r *Repo) CreateGroup(ctx context.Context, g models.CustomerGroup) (models.CustomerGroup, error) {
	if err := r.ready(); err != nil {
		return models.CustomerGroup{}, err
	}

	meta, err := fromMetadata(g.Metadata)
	if err != nil {
		return models.CustomerGroup{}, err
	}

	row, err := r.q.InsertCustomerGroup(ctx, customerdb.InsertCustomerGroupParams{
		ID:        g.ID,
		Name:      g.Name,
		Metadata:  meta,
		CreatedAt: fromTime(g.CreatedAt),
	})
	if err != nil {
		if ConstraintName(err) == IndexGroupName {
			return models.CustomerGroup{}, errors.Wrap(err, errors.KindConflict, CodeGroupNameTaken,
				"%q adında bir müşteri grubu zaten var", g.Name)
		}
		return models.CustomerGroup{}, wrapDB(err, "müşteri grubu oluşturulamadı")
	}
	return toGroup(row)
}

// GetGroup kimliğe göre grup döner; yoksa errors.NotFound.
func (r *Repo) GetGroup(ctx context.Context, id string) (models.CustomerGroup, error) {
	if err := r.ready(); err != nil {
		return models.CustomerGroup{}, err
	}

	row, err := r.q.GetCustomerGroup(ctx, id)
	if err != nil {
		return models.CustomerGroup{}, notFoundOr(err, CodeGroupNotFound, "müşteri grubu bulunamadı: %s", id)
	}
	return toGroup(row)
}

// ListGroups sayfalanmış grup listesini ve TOPLAM kayıt sayısını döner.
func (r *Repo) ListGroups(ctx context.Context, limit, offset int64) ([]models.CustomerGroup, int64, error) {
	if err := r.ready(); err != nil {
		return nil, 0, err
	}

	rows, err := r.q.ListCustomerGroups(ctx, customerdb.ListCustomerGroupsParams{
		Lim: toInt32(limit),
		Off: toInt32(offset),
	})
	if err != nil {
		return nil, 0, wrapDB(err, "müşteri grubu listesi alınamadı")
	}

	total, err := r.q.CountCustomerGroups(ctx)
	if err != nil {
		return nil, 0, wrapDB(err, "müşteri grubu sayısı alınamadı")
	}

	groups, err := toGroups(rows)
	if err != nil {
		return nil, 0, err
	}
	return groups, total, nil
}

// UpdateGroup grubun verilen alanlarını günceller; yoksa errors.NotFound.
//
// Yeni ad başka bir CANLI grup tarafından kullanılıyorsa errors.Conflict döner;
// kural veritabanındaki kısmi benzersiz indekstedir (bkz. [IndexGroupName]) ve
// uygulama tarafında tekrarlanmaz.
func (r *Repo) UpdateGroup(
	ctx context.Context,
	id string,
	patch models.CustomerGroupPatch,
	now time.Time,
) (models.CustomerGroup, error) {
	if err := r.ready(); err != nil {
		return models.CustomerGroup{}, err
	}

	meta, err := patchMetadata(patch.Metadata)
	if err != nil {
		return models.CustomerGroup{}, err
	}

	row, err := r.q.UpdateCustomerGroup(ctx, customerdb.UpdateCustomerGroupParams{
		ID:        id,
		Name:      patch.Name,
		Metadata:  meta,
		UpdatedAt: fromTime(now),
	})
	if err != nil {
		if ConstraintName(err) == IndexGroupName {
			return models.CustomerGroup{}, errors.Wrap(err, errors.KindConflict, CodeGroupNameTaken,
				"bu adda bir müşteri grubu zaten var")
		}
		return models.CustomerGroup{}, notFoundOr(err, CodeGroupNotFound,
			"müşteri grubu bulunamadı: %s", id)
	}
	return toGroup(row)
}

// DeleteGroup grubu soft delete ile siler; yoksa errors.NotFound.
//
// Üyelik satırları BIRAKILIR ve bu bilinçlidir: grubu okuyan her sorgu
// deleted_at IS NULL süzer, dolayısıyla silinmiş bir grup ne müşterinin
// gruplarında ne de grup süzgeçli müşteri listelemesinde görünür. Satırlar,
// kayıt bir gün gerçekten silindiğinde cascade ile gider.
func (r *Repo) DeleteGroup(ctx context.Context, id string, now time.Time) error {
	if err := r.ready(); err != nil {
		return err
	}

	if _, err := r.q.SoftDeleteCustomerGroup(ctx, customerdb.SoftDeleteCustomerGroupParams{
		ID:        id,
		DeletedAt: fromTime(now),
	}); err != nil {
		return notFoundOr(err, CodeGroupNotFound, "müşteri grubu bulunamadı: %s", id)
	}
	return nil
}

// AddToGroup müşteriyi gruba ekler; üyelik zaten varsa hiçbir şey yapmaz.
//
// Müşteri ve grup varlığı AYNI işlemde, üyelik yazımından önce doğrulanır:
// foreign key ihlali de aynı sonucu verirdi ama hangi tarafın (müşteri mi grup
// mu) eksik olduğunu söylemezdi ve istemciye 422 olarak dönerdi; eksik bir
// kaynak için doğru sınıf errors.NotFound'dur.
func (r *Repo) AddToGroup(ctx context.Context, customerID, groupID string, now time.Time) error {
	return r.inTx(ctx, func(q *customerdb.Queries) error {
		if _, err := q.GetCustomer(ctx, customerID); err != nil {
			return notFoundOr(err, CodeCustomerNotFound, "müşteri bulunamadı: %s", customerID)
		}
		if _, err := q.GetCustomerGroup(ctx, groupID); err != nil {
			return notFoundOr(err, CodeGroupNotFound, "müşteri grubu bulunamadı: %s", groupID)
		}

		if err := q.AddCustomerToGroup(ctx, customerdb.AddCustomerToGroupParams{
			CustomerID:      customerID,
			CustomerGroupID: groupID,
			CreatedAt:       fromTime(now),
		}); err != nil {
			return wrapDB(err, "müşteri gruba eklenemedi")
		}
		return nil
	})
}

// RemoveFromGroup müşteriyi gruptan çıkarır; üyelik yoksa errors.NotFound.
//
// Silinen satır sayısı olmadan bu ayrım yapılamazdı: DELETE hiçbir satıra
// dokunmadığında da hatasız döner ve çağıran, hiç var olmamış bir üyeliği
// kaldırdığını sanırdı.
func (r *Repo) RemoveFromGroup(ctx context.Context, customerID, groupID string) error {
	if err := r.ready(); err != nil {
		return err
	}

	affected, err := r.q.RemoveCustomerFromGroup(ctx, customerdb.RemoveCustomerFromGroupParams{
		CustomerID:      customerID,
		CustomerGroupID: groupID,
	})
	if err != nil {
		return wrapDB(err, "müşteri gruptan çıkarılamadı")
	}
	if affected == 0 {
		return errors.NotFound(CodeMembershipNotFound,
			"%s müşterisi %s grubunun üyesi değil", customerID, groupID)
	}
	return nil
}

// ListGroupsOf müşterinin üyesi olduğu grupları döner.
func (r *Repo) ListGroupsOf(ctx context.Context, customerID string) ([]models.CustomerGroup, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}

	rows, err := r.q.ListGroupsOfCustomer(ctx, customerID)
	if err != nil {
		return nil, wrapDB(err, "müşterinin grupları alınamadı: %s", customerID)
	}
	return toGroups(rows)
}

// GroupIDsOfCustomers birden çok müşterinin grup kimliklerini TEK sorguda
// döner.
//
// Sonuç, customer idnden grup kimliklerine bir haritadır. Hiç grubu olmayan
// müşteri için ANAHTAR BULUNMAZ; çağıran nil dilimi boş dilim gibi
// kullanabilir. Query sağlayıcısı bunu batch olarak çağırır ve müşteri başına
// ayrı sorgu yapmaz (ADR 0004'ün N+1 yasağı).
func (r *Repo) GroupIDsOfCustomers(ctx context.Context, customerIDs []string) (map[string][]string, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if len(customerIDs) == 0 {
		return map[string][]string{}, nil
	}

	rows, err := r.q.ListGroupIDsOfCustomers(ctx, customerIDs)
	if err != nil {
		return nil, wrapDB(err, "müşterilerin grup kimlikleri alınamadı")
	}

	out := make(map[string][]string, len(customerIDs))
	for _, row := range rows {
		out[row.CustomerID] = append(out[row.CustomerID], row.CustomerGroupID)
	}
	return out, nil
}

// toGroup üretilen satırı domain modeline çevirir.
func toGroup(row customerdb.CustomerGroup) (models.CustomerGroup, error) {
	meta, err := toMetadata(row.Metadata)
	if err != nil {
		return models.CustomerGroup{}, err
	}
	return models.CustomerGroup{
		ID:        row.ID,
		Name:      row.Name,
		Metadata:  meta,
		CreatedAt: toTime(row.CreatedAt),
		UpdatedAt: toTime(row.UpdatedAt),
		DeletedAt: toTimePtr(row.DeletedAt),
	}, nil
}

// toGroups satır dilimini domain modellerine çevirir.
func toGroups(rows []customerdb.CustomerGroup) ([]models.CustomerGroup, error) {
	out := make([]models.CustomerGroup, 0, len(rows))
	for i := range rows {
		g, err := toGroup(rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, nil
}
