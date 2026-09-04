package api

import (
	"time"

	"github.com/bdrtr/gobit/internal/modules/notification/models"
)

// The DTOs are kept SEPARATE from the domain models: the JSON field names are
// the external contract, and a rename made in the model must not break the
// client.

// deliveryDTO is the response body of a delivery log record.
//
// THERE IS NO RECIPIENT ADDRESS FIELD and none can be added: the record does
// not carry one either (see the migration). The only source to invent one from
// would be the order itself, and this endpoint would turn into a door serving
// personal data out of a second place.
type deliveryDTO struct {
	// ID is the identifier of the record.
	ID string `json:"id"`
	// Template is the template of the notification sent.
	Template string `json:"template"`
	// Channel is the send channel ("email" | "sms").
	Channel string `json:"channel"`
	// Reference is the identifier of the order the notification is bound to.
	Reference string `json:"reference"`
	// ProviderID is the identifier of the provider that performed the send.
	ProviderID string `json:"provider_id"`
	// Status is the outcome of the attempt: pending | sent | failed | skipped.
	Status string `json:"status"`
	// Error is filled only while status is "failed"; when empty the field
	// does not show up at all.
	Error string `json:"error,omitempty"`
	// CreatedAt is the moment the send was attempted (RFC3339, UTC).
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is the moment the outcome was written (RFC3339, UTC).
	UpdatedAt time.Time `json:"updated_at"`
}

// toDeliveryDTO converts the domain record into the response body.
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
