package service

import (
	"context"
	"encoding/json"
	"math"
	"strings"

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

// OpenReturnParcel opens a parcel on a return option bringing back the order
// return returnID names, holding quantities[i] units of the order line
// lineIDs[i], and reports whether the key had already opened it (ADR 0413,
// ADR 0384). The bound on what the return still awaits and the option's
// direction are the service's; the two lists travel side by side because the
// panel cannot name this module's types. The provider is handed no
// destination: where a return parcel comes from is the customer's to say.
func (a *AdminSurface) OpenReturnParcel(
	ctx context.Context, orderID, returnID, optionID, idempotencyKey string,
	lineIDs []string, quantities []int64,
) (fulfillmentID string, alreadyOpen bool, err error) {
	if strings.TrimSpace(returnID) == "" {
		return "", false, errors.Invalid(CodeInvalidInput,
			"a return's parcel names the return it brings back")
	}
	if len(lineIDs) != len(quantities) {
		return "", false, errors.Invalid(CodeInvalidInput,
			"%d lines and %d quantities were given; each line takes one quantity",
			len(lineIDs), len(quantities))
	}
	items := make([]FulfillmentItemInput, 0, len(lineIDs))
	for i := range lineIDs {
		items = append(items, FulfillmentItemInput{LineItemID: lineIDs[i], Quantity: quantities[i]})
	}

	alreadyOpen = a.svc.isRetry(ctx, strings.TrimSpace(idempotencyKey))
	ful, err := a.svc.CreateFulfillment(ctx, CreateFulfillmentInput{
		Reference: orderID, ShippingOptionID: optionID, IdempotencyKey: idempotencyKey,
		ReturnID: returnID, Items: items,
	})
	if err != nil {
		return "", false, err
	}

	return ful.ID, alreadyOpen, nil
}

// returnOptionChoice is one return option the panel's form offers; the json
// tags are the contract with the panel, which cannot import this package.
type returnOptionChoice struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ReturnOptionsJSON lists the return options a region offers in a currency,
// admin-only ones included (ADR 0413). No cart stands behind a return, so the
// facts a rule reads are not given: an option ruled on them is not listed, and
// the API still opens a parcel on it.
func (a *AdminSurface) ReturnOptionsJSON(ctx context.Context, regionID, currencyCode string) (json.RawMessage, error) {
	quoted, err := a.svc.ListShippingOptionsFor(ctx, ListOptionsInput{
		RegionID: regionID, CurrencyCode: currencyCode,
		IsReturn: true, IncludeAdminOnly: true, TrustedFacts: true,
	})
	if err != nil {
		return nil, err
	}
	choices := make([]returnOptionChoice, 0, len(quoted))
	for i := range quoted {
		choices = append(choices, returnOptionChoice{ID: quoted[i].Option.ID, Name: quoted[i].Option.Name})
	}
	body, err := json.Marshal(choices)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, codeAdminEncodeFailed,
			"the return options could not be encoded")
	}

	return body, nil
}

// ReviseShippingOption writes the option's name, fee, storefront visibility
// and delivery days from the ones the operator read, and refuses when another
// writer changed any of them since (ADR 0333, ADR 0421); the fee is in the
// currency's minor units, adminOnly keeps the option off the storefront, and
// the days are business days, both nil for none.
func (a *AdminSurface) ReviseShippingOption(
	ctx context.Context, id, readName string, readAmount int64, readAdminOnly bool,
	readMinDays, readMaxDays *int64,
	name string, amount int64, adminOnly bool, minDays, maxDays *int64,
) error {
	readDays, err := deliveryDaysOf(readMinDays, readMaxDays)
	if err != nil {
		return err
	}
	days, err := deliveryDaysOf(minDays, maxDays)
	if err != nil {
		return err
	}
	_, err = a.svc.ReviseShippingOption(ctx, id,
		models.OptionTerms{Name: readName, Amount: readAmount, AdminOnly: readAdminOnly, DeliveryDays: readDays},
		models.OptionTerms{Name: name, Amount: amount, AdminOnly: adminOnly, DeliveryDays: days})

	return err
}

// deliveryDaysOf reads the panel's two day figures as an option's days: both
// nil is none, one alone is refused, and each is held to the range an int32
// carries before the service holds it to an option's (ADR 0421).
func deliveryDaysOf(minDays, maxDays *int64) (*models.DeliveryDays, error) {
	switch {
	case minDays == nil && maxDays == nil:
		return nil, nil
	case minDays == nil || maxDays == nil:
		return nil, errors.Invalid(CodeInvalidInput,
			"an option's delivery days are a minimum and a maximum given together")
	case *minDays < math.MinInt32 || *minDays > math.MaxInt32 || *maxDays < math.MinInt32 || *maxDays > math.MaxInt32:
		return nil, errors.Invalid(CodeInvalidInput,
			"an option's delivery days are at most %d: %d to %d given", models.MaxDeliveryDays, *minDays, *maxDays)
	}

	return &models.DeliveryDays{Min: int32(*minDays), Max: int32(*maxDays)}, nil
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
// offers it in every region, adminOnly keeps it off the storefront, and the
// days are how many business days its delivery takes, both nil for none (ADR
// 0421).
func (a *AdminSurface) CreateShippingOption(
	ctx context.Context, name, providerID, profileID, priceType string, amount int64,
	currency, regionID string, isReturn, adminOnly bool, minDays, maxDays *int64,
) (string, error) {
	days, err := deliveryDaysOf(minDays, maxDays)
	if err != nil {
		return "", err
	}
	option, err := a.svc.CreateShippingOption(ctx, CreateOptionInput{
		Name: name, ProviderID: providerID, ShippingProfileID: profileID, PriceType: priceType,
		Amount: amount, CurrencyCode: currency, RegionID: regionID, IsReturn: isReturn, AdminOnly: adminOnly,
		DeliveryDays: days,
	})
	if err != nil {
		return "", err
	}

	return option.ID, nil
}
