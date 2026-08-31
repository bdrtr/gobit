package api

import (
	"time"

	"github.com/bdrtr/gobit/internal/modules/notification/models"
)

// DTO'lar domain modellerinden AYRI tutulur: JSON alan adları dış sözleşmedir
// ve modelde yapılan bir yeniden adlandırma istemciyi kırmamalıdır.

// deliveryDTO bir teslim günlüğü kaydının yanıt gövdesidir.
//
// ALICI ADRESİ ALANI YOKTUR ve eklenemez: kayıtta da yoktur (bkz. migration).
// Uydurulacak tek kaynak siparişin kendisi olurdu ve bu uç, kişisel veriyi
// ikinci bir yerden servis eden bir kapıya dönüşürdü.
type deliveryDTO struct {
	// ID kaydın kimliğidir.
	ID string `json:"id"`
	// Template gönderilen bildirimin şablonudur.
	Template string `json:"template"`
	// Channel gönderim kanalıdır ("email" | "sms").
	Channel string `json:"channel"`
	// Reference bildirimin bağlı olduğu siparişin kimliğidir.
	Reference string `json:"reference"`
	// ProviderID gönderimi yapan sağlayıcının kimliğidir.
	ProviderID string `json:"provider_id"`
	// Status denemenin sonucudur: pending | sent | failed | skipped.
	Status string `json:"status"`
	// Error yalnızca status "failed" iken doludur; boşsa alan hiç görünmez.
	Error string `json:"error,omitempty"`
	// CreatedAt gönderimin denendiği andır (RFC3339, UTC).
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt sonucun yazıldığı andır (RFC3339, UTC).
	UpdatedAt time.Time `json:"updated_at"`
}

// toDeliveryDTO domain kaydını yanıt gövdesine çevirir.
func toDeliveryDTO(d models.Delivery) deliveryDTO {
	return deliveryDTO{
		ID:         d.ID,
		Template:   d.Template,
		Channel:    d.Channel,
		Reference:  d.Reference,
		ProviderID: d.ProviderID,
		Status:     d.Status.String(),
		Error:      d.Error,
		CreatedAt:  d.CreatedAt,
		UpdatedAt:  d.UpdatedAt,
	}
}
