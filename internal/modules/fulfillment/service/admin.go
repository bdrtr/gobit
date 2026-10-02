package service

import (
	"context"
	"encoding/json"

	"github.com/bdrtr/gobit/core/errors"
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

// codeAdminEncodeFailed reports choices the panel's surface could not encode.
const codeAdminEncodeFailed = "fulfillment_admin_encode_failed"

// adminChoices is what the panel's form writes a shipping option from (ADR
// 0334); the json tags are the contract with the panel, which cannot import
// this package.
type adminChoices struct {
	Providers    []string       `json:"providers"`
	Profiles     []adminProfile `json:"profiles"`
	ProfilesMore bool           `json:"profiles_more"`
}

// adminProfile is one shipping profile the form offers.
type adminProfile struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// OptionChoicesJSON lists what a shipping option is written from: the
// providers registered in this installation, and the shipping profiles a
// page at a time, the most recent first, saying when there are more (ADR
// 0334).
func (a *AdminSurface) OptionChoicesJSON(ctx context.Context) (json.RawMessage, error) {
	profiles, total, err := a.svc.ListShippingProfiles(ctx, ListProfilesInput{Page: Page{Limit: MaxLimit}})
	if err != nil {
		return nil, err
	}
	choices := adminChoices{
		Providers: a.svc.ProviderIDs(ctx), Profiles: make([]adminProfile, 0, len(profiles)),
		ProfilesMore: total > int64(len(profiles)),
	}
	for _, profile := range profiles {
		choices.Profiles = append(choices.Profiles, adminProfile{
			ID: profile.ID, Name: profile.Name, Type: string(profile.Type),
		})
	}
	body, err := json.Marshal(choices)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, codeAdminEncodeFailed,
			"the shipping option choices could not be encoded")
	}

	return body, nil
}

// CreateShippingOption writes a shipping option and returns its id (ADR
// 0334): priceType is "flat", whose fee is amount in the currency's minor
// units, or "calculated", whose fee its provider quotes; an empty region
// offers it in every region, and adminOnly keeps it off the storefront.
func (a *AdminSurface) CreateShippingOption(
	ctx context.Context, name, providerID, profileID, priceType string, amount int64,
	currency, regionID string, isReturn, adminOnly bool,
) (string, error) {
	option, err := a.svc.CreateShippingOption(ctx, CreateOptionInput{
		Name: name, ProviderID: providerID, ShippingProfileID: profileID, PriceType: priceType,
		Amount: amount, CurrencyCode: currency, RegionID: regionID, IsReturn: isReturn, AdminOnly: adminOnly,
	})
	if err != nil {
		return "", err
	}

	return option.ID, nil
}
