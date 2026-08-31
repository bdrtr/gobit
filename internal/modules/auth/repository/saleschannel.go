package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
	"github.com/bdrtr/gobit/internal/modules/auth/repository/authdb"
)

// CreateSalesChannel yeni bir satış kanalı yazar.
//
// Ad zaten kullanılıyorsa errors.Conflict döner; kural veritabanındaki kısmi
// benzersiz indekstedir (bkz. [IndexChannelName]).
func (r *Repo) CreateSalesChannel(ctx context.Context, c models.SalesChannel) (models.SalesChannel, error) {
	if err := r.ready(); err != nil {
		return models.SalesChannel{}, err
	}

	meta, err := fromMetadata(c.Metadata)
	if err != nil {
		return models.SalesChannel{}, err
	}

	row, err := r.q.InsertSalesChannel(ctx, authdb.InsertSalesChannelParams{
		ID:          c.ID,
		Name:        c.Name,
		Description: c.Description,
		IsDisabled:  c.IsDisabled,
		Metadata:    meta,
		CreatedAt:   fromTime(c.CreatedAt),
	})
	if err != nil {
		return models.SalesChannel{}, classifyChannelWrite(err, c.Name, "satış kanalı oluşturulamadı")
	}
	return toSalesChannel(row)
}

// GetSalesChannel kimliğe göre kanal döner; yoksa errors.NotFound.
func (r *Repo) GetSalesChannel(ctx context.Context, id string) (models.SalesChannel, error) {
	if err := r.ready(); err != nil {
		return models.SalesChannel{}, err
	}

	row, err := r.q.GetSalesChannel(ctx, id)
	if err != nil {
		return models.SalesChannel{}, notFoundOr(err, CodeSalesChannelNotFound,
			"satış kanalı bulunamadı: %s", id)
	}
	return toSalesChannel(row)
}

// ListSalesChannels süzgeçlenmiş ve sayfalanmış kanal listesini, filtreye uyan
// TOPLAM kayıt sayısıyla birlikte döner.
func (r *Repo) ListSalesChannels(
	ctx context.Context,
	filter models.SalesChannelFilter,
	limit, offset int64,
) ([]models.SalesChannel, int64, error) {
	if err := r.ready(); err != nil {
		return nil, 0, err
	}

	rows, err := r.q.ListSalesChannels(ctx, authdb.ListSalesChannelsParams{
		Name:       filter.Name,
		IsDisabled: filter.IsDisabled,
		Lim:        toInt32(limit),
		Off:        toInt32(offset),
	})
	if err != nil {
		return nil, 0, wrapDB(err, "satış kanalı listesi alınamadı")
	}

	total, err := r.q.CountSalesChannels(ctx, authdb.CountSalesChannelsParams{
		Name:       filter.Name,
		IsDisabled: filter.IsDisabled,
	})
	if err != nil {
		return nil, 0, wrapDB(err, "satış kanalı sayısı alınamadı")
	}

	channels, err := toSalesChannels(rows)
	if err != nil {
		return nil, 0, err
	}
	return channels, total, nil
}

// GetSalesChannelsByIDs verilen kimliklere karşılık gelen kanalları TEK
// sorguda döner. Bulunamayan kimlik için kayıt dönmez; bu bir hata değildir.
func (r *Repo) GetSalesChannelsByIDs(ctx context.Context, ids []string) ([]models.SalesChannel, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []models.SalesChannel{}, nil
	}

	rows, err := r.q.ListSalesChannelsByIDs(ctx, ids)
	if err != nil {
		return nil, wrapDB(err, "satış kanalları alınamadı")
	}
	return toSalesChannels(rows)
}

// UpdateSalesChannel kanalın verilen alanlarını günceller.
func (r *Repo) UpdateSalesChannel(
	ctx context.Context,
	id string,
	patch models.SalesChannelPatch,
	now time.Time,
) (models.SalesChannel, error) {
	if err := r.ready(); err != nil {
		return models.SalesChannel{}, err
	}

	meta, err := patchMetadata(patch.Metadata)
	if err != nil {
		return models.SalesChannel{}, err
	}

	row, err := r.q.UpdateSalesChannel(ctx, authdb.UpdateSalesChannelParams{
		Name:        patch.Name,
		Description: patch.Description,
		IsDisabled:  patch.IsDisabled,
		Metadata:    meta,
		UpdatedAt:   fromTime(now),
		ID:          id,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.SalesChannel{}, errors.NotFound(CodeSalesChannelNotFound,
				"satış kanalı bulunamadı: %s", id)
		}
		return models.SalesChannel{}, classifyChannelWrite(err, derefOr(patch.Name),
			"satış kanalı güncellenemedi")
	}
	return toSalesChannel(row)
}

// DeleteSalesChannel kanalı yumuşak siler ve anahtar bağlarını kaldırır.
//
// Bağlar AYNI işlemde silinir: yumuşak silme bir UPDATE olduğu için foreign
// key CASCADE devreye girmez ve publishable anahtarlar silinmiş bir kanala
// bağlı kalırdı.
func (r *Repo) DeleteSalesChannel(ctx context.Context, id string, now time.Time) error {
	if err := r.ready(); err != nil {
		return err
	}

	return r.inTx(ctx, func(q *authdb.Queries) error {
		if _, err := q.SoftDeleteSalesChannel(ctx, authdb.SoftDeleteSalesChannelParams{
			ID:        id,
			DeletedAt: fromTime(now),
		}); err != nil {
			return notFoundOr(err, CodeSalesChannelNotFound, "satış kanalı bulunamadı: %s", id)
		}
		if err := q.DeleteLinksOfSalesChannel(ctx, id); err != nil {
			return wrapDB(err, "satış kanalının anahtar bağları silinemedi")
		}
		return nil
	})
}

// classifyChannelWrite bir yazma hatasını ad çakışması bakımından sınıflar.
func classifyChannelWrite(err error, name, message string) error {
	if err == nil {
		return nil
	}
	if ConstraintName(err) == IndexChannelName {
		return errors.Wrap(err, errors.KindConflict, CodeChannelNameTaken,
			"%q adlı bir satış kanalı zaten var", name)
	}
	return wrapDB(err, "%s", message)
}
