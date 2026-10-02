package service

import (
	"context"

	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
)

// AdminSurface is the fulfillment module's panel surface (ADR 0324): the moves
// an operator makes on a parcel once it is open. Only primitives cross it, as
// across every surface the panel resolves (ADR 0001), and each move is the
// service's own, so the panel moves a parcel under the state machine the API
// does and is refused where it is.
type AdminSurface struct {
	svc *Service
}

// NewAdminSurface builds the panel's surface over the service.
func NewAdminSurface(svc *Service) *AdminSurface { return &AdminSurface{svc: svc} }

// ShipParcel records that the carrier took the parcel, with its tracking
// number and page when the operator has them.
func (a *AdminSurface) ShipParcel(ctx context.Context, id, trackingNumber, trackingURL string) error {
	_, err := a.svc.MarkShipped(ctx, id, trackingNumber, trackingURL)
	return err
}

// DeliverParcel records that the parcel reached the recipient.
func (a *AdminSurface) DeliverParcel(ctx context.Context, id string) error {
	_, err := a.svc.MarkDelivered(ctx, id)
	return err
}

// ReturnParcel records that the parcel came back to the shop undelivered.
func (a *AdminSurface) ReturnParcel(ctx context.Context, id string) error {
	_, err := a.svc.MarkReturned(ctx, id)
	return err
}

// CancelParcel cancels a parcel that has not left.
func (a *AdminSurface) CancelParcel(ctx context.Context, id string) error {
	return a.svc.CancelFulfillment(ctx, id)
}

// ReviseShippingOption writes the option's name, fee and storefront
// visibility from the ones the operator read, and refuses when another writer
// changed any of them since (ADR 0333); the fee is in the currency's minor
// units, and adminOnly keeps the option off the storefront.
func (a *AdminSurface) ReviseShippingOption(
	ctx context.Context, id, readName string, readAmount int64, readAdminOnly bool,
	name string, amount int64, adminOnly bool,
) error {
	_, err := a.svc.ReviseShippingOption(ctx, id,
		models.OptionTerms{Name: readName, Amount: readAmount, AdminOnly: readAdminOnly},
		models.OptionTerms{Name: name, Amount: amount, AdminOnly: adminOnly})

	return err
}
