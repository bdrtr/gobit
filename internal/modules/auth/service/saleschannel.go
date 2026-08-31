package service

import (
	"context"
	"log/slog"

	"github.com/bdrtr/gobit/internal/modules/auth/models"
)

// SalesChannelInput bir satış kanalının yazma girdisidir.
type SalesChannelInput struct {
	// Name kanalın görünen adıdır; zorunludur ve canlı kanallar arasında
	// benzersizdir.
	Name string
	// Description kanalın açıklamasıdır; boş bırakılabilir.
	Description string
	// IsDisabled kanalın devre dışı açılmasını sağlar.
	IsDisabled bool
	// Metadata serbest yapısal bağlamdır; boş bırakılabilir.
	Metadata map[string]any
}

// CreateSalesChannel yeni bir satış kanalı oluşturur.
//
// Ad zaten kullanılıyorsa errors.Conflict döner.
func (s *Service) CreateSalesChannel(ctx context.Context, in SalesChannelInput) (models.SalesChannel, error) {
	if err := s.ready(); err != nil {
		return models.SalesChannel{}, err
	}
	if err := requireText("satış kanalı adı", in.Name); err != nil {
		return models.SalesChannel{}, err
	}
	if err := checkLen("satış kanalı adı", in.Name, models.MaxNameLen); err != nil {
		return models.SalesChannel{}, err
	}
	if err := checkLen("satış kanalı açıklaması", in.Description, models.MaxDescriptionLen); err != nil {
		return models.SalesChannel{}, err
	}

	now := s.clock()
	created, err := s.repo.CreateSalesChannel(ctx, models.SalesChannel{
		ID:          models.NewSalesChannelID(now),
		Name:        in.Name,
		Description: in.Description,
		IsDisabled:  in.IsDisabled,
		Metadata:    in.Metadata,
		CreatedAt:   now,
	})
	if err != nil {
		return models.SalesChannel{}, err
	}

	s.log.InfoContext(ctx, "satış kanalı oluşturuldu",
		slog.String("sales_channel_id", created.ID),
	)
	return created, nil
}

// GetSalesChannel kimliğe göre kanal döner; yoksa errors.NotFound.
func (s *Service) GetSalesChannel(ctx context.Context, id string) (models.SalesChannel, error) {
	if err := s.ready(); err != nil {
		return models.SalesChannel{}, err
	}
	if err := requireID(id, models.SalesChannelIDPrefix, "satış kanalı kimliği"); err != nil {
		return models.SalesChannel{}, err
	}
	return s.repo.GetSalesChannel(ctx, id)
}

// ListSalesChannelsInput kanal listelemesinin girdisidir.
type ListSalesChannelsInput struct {
	// Name verilirse yalnızca bu ada sahip kanal döner.
	Name *string
	// IsDisabled verilirse devre dışı/etkin ayrımına göre süzer.
	IsDisabled *bool
	// Limit sayfa boyudur; 0 ise [DefaultLimit] uygulanır.
	Limit int64
	// Offset atlanacak kayıt sayısıdır.
	Offset int64
}

// ListSalesChannels süzgeçlenmiş ve sayfalanmış kanal listesini döner.
func (s *Service) ListSalesChannels(
	ctx context.Context,
	in ListSalesChannelsInput,
) (Page[models.SalesChannel], error) {
	if err := s.ready(); err != nil {
		return Page[models.SalesChannel]{}, err
	}

	limit, offset, err := normalizePaging(in.Limit, in.Offset)
	if err != nil {
		return Page[models.SalesChannel]{}, err
	}

	items, total, err := s.repo.ListSalesChannels(ctx, models.SalesChannelFilter{
		Name:       in.Name,
		IsDisabled: in.IsDisabled,
	}, limit, offset)
	if err != nil {
		return Page[models.SalesChannel]{}, err
	}
	return Page[models.SalesChannel]{Items: items, Count: total, Limit: limit, Offset: offset}, nil
}

// UpdateSalesChannelInput bir kanalın kısmi güncelleme girdisidir.
//
// nil alan "dokunma", dolu alan "bu değeri yaz" demektir.
type UpdateSalesChannelInput struct {
	// Name kanalın yeni adıdır.
	Name *string
	// Description kanalın yeni açıklamasıdır.
	Description *string
	// IsDisabled kanalın yeni etkinlik durumudur.
	//
	// Kanalı devre dışı bırakmak, ona bağlı publishable anahtarların o kanalı
	// GÖRMEMESİ demektir; başka kanalı olmayan bir anahtar bu işlemden sonra
	// mağaza kimliği kuramaz (bkz. [Service.authenticatePublishable]).
	IsDisabled *bool
	// Metadata yeni metadata haritasıdır; sütunun tamamını değiştirir.
	Metadata map[string]any
}

// UpdateSalesChannel kanalın verilen alanlarını günceller.
func (s *Service) UpdateSalesChannel(
	ctx context.Context,
	id string,
	in UpdateSalesChannelInput,
) (models.SalesChannel, error) {
	if err := s.ready(); err != nil {
		return models.SalesChannel{}, err
	}
	if err := requireID(id, models.SalesChannelIDPrefix, "satış kanalı kimliği"); err != nil {
		return models.SalesChannel{}, err
	}
	if in.Name != nil {
		if err := requireText("satış kanalı adı", *in.Name); err != nil {
			return models.SalesChannel{}, err
		}
		if err := checkLen("satış kanalı adı", *in.Name, models.MaxNameLen); err != nil {
			return models.SalesChannel{}, err
		}
	}
	if in.Description != nil {
		if err := checkLen("satış kanalı açıklaması", *in.Description, models.MaxDescriptionLen); err != nil {
			return models.SalesChannel{}, err
		}
	}

	return s.repo.UpdateSalesChannel(ctx, id, models.SalesChannelPatch{
		Name:        in.Name,
		Description: in.Description,
		IsDisabled:  in.IsDisabled,
		Metadata:    in.Metadata,
	}, s.clock())
}

// DeleteSalesChannel kanalı yumuşak siler ve anahtar bağlarını kaldırır.
//
// Bağların kaldırılması şarttır: yumuşak silme bir UPDATE olduğu için foreign
// key CASCADE devreye girmez ve publishable anahtarlar silinmiş bir kanala
// bağlı görünmeye devam ederdi.
func (s *Service) DeleteSalesChannel(ctx context.Context, id string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := requireID(id, models.SalesChannelIDPrefix, "satış kanalı kimliği"); err != nil {
		return err
	}
	if err := s.repo.DeleteSalesChannel(ctx, id, s.clock()); err != nil {
		return err
	}

	s.log.InfoContext(ctx, "satış kanalı silindi", slog.String("sales_channel_id", id))
	return nil
}
