package service

import (
	"context"
	"log/slog"
	"strings"

	"github.com/bdrtr/gobit/internal/modules/customer/models"
)

// GroupInput bir müşteri grubunun yazma girdisidir.
type GroupInput struct {
	// Name grubun görünen adıdır; zorunludur ve canlı gruplar arasında
	// benzersizdir.
	Name string
	// Metadata serbest yapısal bağlamdır; boş bırakılabilir.
	Metadata map[string]any
}

// CreateGroup yeni bir müşteri grubu oluşturur.
//
// Aynı adda canlı bir grup varsa errors.Conflict döner; kural veritabanındaki
// kısmi benzersiz indekstedir.
func (s *Service) CreateGroup(ctx context.Context, in GroupInput) (models.CustomerGroup, error) {
	if err := s.ready(); err != nil {
		return models.CustomerGroup{}, err
	}
	if err := requireText("grup adı", in.Name); err != nil {
		return models.CustomerGroup{}, err
	}
	name := strings.TrimSpace(in.Name)
	if err := checkLen("grup adı", name, models.MaxNameLen); err != nil {
		return models.CustomerGroup{}, err
	}

	now := s.clock()
	return s.repo.CreateGroup(ctx, models.CustomerGroup{
		ID:        models.NewCustomerGroupID(now),
		Name:      name,
		Metadata:  in.Metadata,
		CreatedAt: now,
	})
}

// UpdateGroupInput bir müşteri grubunun kısmi güncelleme girdisidir.
//
// nil alan "dokunma", dolu alan "bu değeri yaz" demektir.
type UpdateGroupInput struct {
	// Name grubun yeni adıdır; verilirse boş olamaz ve canlı gruplar arasında
	// benzersizdir.
	Name *string
	// Metadata yeni metadata haritasıdır; sütunun tamamını değiştirir.
	Metadata map[string]any
}

// UpdateGroup grubun verilen alanlarını günceller; yoksa errors.NotFound.
//
// Aynı adda başka bir canlı grup varsa errors.Conflict döner. Ad verilirse BOŞ
// OLAMAZ: kısmi güncelleme bir alanı atlayabilir ama var olan bir zorunluluğu
// kaldıramaz.
func (s *Service) UpdateGroup(ctx context.Context, id string, in UpdateGroupInput) (models.CustomerGroup, error) {
	if err := s.ready(); err != nil {
		return models.CustomerGroup{}, err
	}
	if err := requireID(id, models.CustomerGroupIDPrefix, "grup kimliği"); err != nil {
		return models.CustomerGroup{}, err
	}

	patch := models.CustomerGroupPatch{Metadata: in.Metadata}
	if in.Name != nil {
		if err := requireText("grup adı", *in.Name); err != nil {
			return models.CustomerGroup{}, err
		}
		name := strings.TrimSpace(*in.Name)
		if err := checkLen("grup adı", name, models.MaxNameLen); err != nil {
			return models.CustomerGroup{}, err
		}
		patch.Name = &name
	}

	return s.repo.UpdateGroup(ctx, id, patch, s.clock())
}

// DeleteGroup grubu soft delete ile siler; yoksa errors.NotFound.
//
// Üyelik satırları KALDIRILMAZ ama silinen grup hiçbir okumada görünmez:
// [Service.ListGroups], [Service.GetGroup], [Service.ListGroupsOf], Query
// sağlayıcısının grup kimlikleri ve grup süzgeçli müşteri listelemesi silinmiş
// grubu ATLAR. Grubun adı da serbest kalır; benzersizlik indeksi yalnızca canlı
// grupları kapsar.
//
// Silme, üyelerin fiyat segmentini değiştirir: pricing'in kural bağlamına
// artık bu grubun kimliği taşınmaz.
func (s *Service) DeleteGroup(ctx context.Context, id string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := requireID(id, models.CustomerGroupIDPrefix, "grup kimliği"); err != nil {
		return err
	}

	if err := s.repo.DeleteGroup(ctx, id, s.clock()); err != nil {
		return err
	}

	s.log.InfoContext(ctx, "müşteri grubu silindi",
		slog.String("customer_group_id", id),
	)
	return nil
}

// GetGroup kimliğe göre grup döner; yoksa errors.NotFound.
func (s *Service) GetGroup(ctx context.Context, id string) (models.CustomerGroup, error) {
	if err := s.ready(); err != nil {
		return models.CustomerGroup{}, err
	}
	if err := requireID(id, models.CustomerGroupIDPrefix, "grup kimliği"); err != nil {
		return models.CustomerGroup{}, err
	}
	return s.repo.GetGroup(ctx, id)
}

// ListGroups sayfalanmış grup listesini döner.
func (s *Service) ListGroups(ctx context.Context, limit, offset int64) (Page[models.CustomerGroup], error) {
	if err := s.ready(); err != nil {
		return Page[models.CustomerGroup]{}, err
	}
	limit, offset, err := normalizePaging(limit, offset)
	if err != nil {
		return Page[models.CustomerGroup]{}, err
	}

	items, total, err := s.repo.ListGroups(ctx, limit, offset)
	if err != nil {
		return Page[models.CustomerGroup]{}, err
	}
	return Page[models.CustomerGroup]{Items: items, Count: total, Limit: limit, Offset: offset}, nil
}

// AddToGroup müşteriyi gruba ekler.
//
// İşlem idempotenttir: zaten üye olan bir müşteri için ikinci çağrı hata
// vermez, çünkü üyelik bir kümedir ve aynı çağrının tekrarı (yeniden deneme,
// çift tıklama) aynı sonucu vermelidir. Müşteri ya da grup yoksa
// errors.NotFound döner.
func (s *Service) AddToGroup(ctx context.Context, customerID, groupID string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := requireID(customerID, models.CustomerIDPrefix, "müşteri kimliği"); err != nil {
		return err
	}
	if err := requireID(groupID, models.CustomerGroupIDPrefix, "grup kimliği"); err != nil {
		return err
	}

	if err := s.repo.AddToGroup(ctx, customerID, groupID, s.clock()); err != nil {
		return err
	}

	s.log.DebugContext(ctx, "müşteri gruba eklendi",
		slog.String("customer_id", customerID),
		slog.String("customer_group_id", groupID),
	)
	return nil
}

// RemoveFromGroup müşteriyi gruptan çıkarır; üyelik yoksa errors.NotFound.
//
// Ekleme idempotent, çıkarma değildir. Ayrım bilinçlidir: olmayan bir üyeliği
// kaldırmak istemcinin yanlış kimlikle çağırdığının en yaygın işaretidir ve
// sessizce başarı dönmek o hatayı gizlerdi.
func (s *Service) RemoveFromGroup(ctx context.Context, customerID, groupID string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := requireID(customerID, models.CustomerIDPrefix, "müşteri kimliği"); err != nil {
		return err
	}
	if err := requireID(groupID, models.CustomerGroupIDPrefix, "grup kimliği"); err != nil {
		return err
	}
	return s.repo.RemoveFromGroup(ctx, customerID, groupID)
}

// ListGroupsOf müşterinin üyesi olduğu grupları döner.
//
// Müşterinin varlığı ÖNCE doğrulanır: olmayan bir müşteri için boş liste
// dönseydi istemci 404 yerine "hiç grubu yok" sanırdı.
func (s *Service) ListGroupsOf(ctx context.Context, customerID string) ([]models.CustomerGroup, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := requireID(customerID, models.CustomerIDPrefix, "müşteri kimliği"); err != nil {
		return nil, err
	}
	if _, err := s.repo.GetCustomer(ctx, customerID); err != nil {
		return nil, err
	}
	return s.repo.ListGroupsOf(ctx, customerID)
}
