package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/bdrtr/gobit/core/errors"
)

// AdminSurface is the notification module's panel surface (ADR 0317): the
// delivery log a page at a time, and the resend of a failed order mail
// (ADR 0386). Only primitives and JSON cross it, as across every surface
// the panel resolves (ADR 0001).
type AdminSurface struct {
	svc *Service
}

// NewAdminSurface builds the panel's surface over the service.
func NewAdminSurface(svc *Service) *AdminSurface { return &AdminSurface{svc: svc} }

// codeAdminEncodeFailed reports a listing the surface could not encode.
const codeAdminEncodeFailed = "notification_admin_encode_failed"

// adminDelivery is one delivery as the panel lists it; the json tags are the
// contract with the panel, which cannot import this package.
type adminDelivery struct {
	ID         string    `json:"id"`
	Template   string    `json:"template"`
	Channel    string    `json:"channel"`
	Reference  string    `json:"reference"`
	Status     string    `json:"status"`
	Error      string    `json:"error"`
	UpdatedAt  time.Time `json:"updated_at"`
	Resendable bool      `json:"resendable"`
}

// DeliveriesJSON lists the deliveries in the status, every status when it is
// empty, and of the order the reference names when it is not empty, newest
// first, a page at a time, with the total. A delivery the operator may send
// again says so.
func (a *AdminSurface) DeliveriesJSON(
	ctx context.Context, status, reference string, limit, offset int32,
) (json.RawMessage, int64, error) {
	if a == nil || a.svc == nil {
		return nil, 0, errors.Unavailable(CodeNotReady, "the notification service is not set up")
	}

	in := ListDeliveriesInput{Page: Page{Limit: int64(limit), Offset: int64(offset)}}
	if status != "" {
		in.Status = &status
	}
	if reference != "" {
		in.Reference = &reference
	}
	records, total, err := a.svc.ListDeliveries(ctx, in)
	if err != nil {
		return nil, 0, err
	}

	out := make([]adminDelivery, 0, len(records))
	for i := range records {
		d := &records[i]
		out = append(out, adminDelivery{
			ID: d.ID, Template: d.Template, Channel: d.Channel, Reference: d.Reference,
			Status: string(d.Status), Error: d.Error, UpdatedAt: d.UpdatedAt,
			Resendable: orderTemplate(d.Template) && resendable(*d),
		})
	}
	body, err := json.Marshal(out)
	if err != nil {
		return nil, 0, errors.Wrap(err, errors.KindInternal, codeAdminEncodeFailed,
			"the deliveries could not be encoded")
	}

	return body, total, nil
}

// ResendDelivery sends the delivery again and returns the status it was left
// in (ADR 0317). A send the provider refuses again is an outcome rather than
// a failure of the request: the record says failed with the provider's reason,
// which the panel lists; a delivery that may not be sent again is refused.
func (a *AdminSurface) ResendDelivery(ctx context.Context, id string) (string, error) {
	if a == nil || a.svc == nil {
		return "", errors.Unavailable(CodeNotReady, "the notification service is not set up")
	}

	record, err := a.svc.ResendDelivery(ctx, id)
	if err != nil && errors.CodeOf(err) != CodeSendFailed {
		return "", err
	}

	return string(record.Status), nil
}
