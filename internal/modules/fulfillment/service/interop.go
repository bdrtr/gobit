package service

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/bdrtr/gobit/core/errors"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
)

// This file is the fulfillment module's CROSS-MODULE surface (ADR 0001,
// ADR 0006).
//
// The cart and order flows under internal/workflows CANNOT import this module.
// The solution is the same as the interop.go in the region/cart/payment/order/
// inventory modules: publish a surface that uses only PRIMITIVE and stdlib
// types. The consumer defines its own narrow interface, this type satisfies it
// STRUCTURALLY, and it is resolved from the container under the name
// "fulfillment.interop".
//
// The reason is Go's structural conformance rule: because the consumer cannot
// import fulfillment, it cannot name a type such as models.Fulfillment in its
// signature; the moment it names one, that becomes ANOTHER type defined in its
// own package and the concrete service no longer satisfies the consumer's
// interface.
//
// The surface is DELIBERATELY narrow and was chosen from what the flows need:
// ask for the shipping options a cart is eligible for along with their prices,
// order the candidate warehouses by preference, open a fulfillment, cancel a
// fulfillment (compensation) and read its status. Every method added here
// raises the cost of extracting fulfillment into a separate service.
//
// # What is NOT here, and why it is not here YET
//
// There is no way for a CARRIER to report an event. Measured on 2026-09-06:
// the five methods below are the module's entire cross-module write surface,
// and none of them can move a shipment to shipped, delivered or returned. The
// admin HTTP routes can, but a carrier's webhook holds no admin credential —
// that is the premise the inbound callback ring is built on (ADR 0028) — and a
// plugin cannot reach [Service.MarkShipped] directly: TestPluginsDoNotImportModules
// refuses the import, and a structural interface naming it cannot be written
// either, because the method returns [models.Fulfillment], a type the plugin's
// package cannot name. So a carrier plugin (C6) has nowhere to deliver what it
// receives.
//
// The method is NOT added here ahead of that plugin, and the reason is the
// rule this repository has paid for twice. A cross-module surface is only
// meaningful against a consumer: this file's own documentation says the JSON
// schema "MUST BE IDENTICAL to the schema on the consumer side, and conformance
// can only be proven by an integration test", so a method with no consumer is a
// contract nothing can check and nothing can correct. The same call was made for
// the inventory event (ADR 0063, which refused a forwarder as a first
// subscriber) and for the plugin job surface, which arrived WITH its first
// consumer rather than before one.
//
// What it should look like when it lands is worth writing down, because the
// shape is not obvious and it was measured rather than guessed. It is ONE
// method, not three, and it carries the carrier's own instant:
//
//	RecordCarrierEvent(ctx context.Context, fulfillmentID, event string, occurredAt time.Time, trackingNumber, trackingURL string) (string, error)
//
// One method because the tolerance is a property of the SEQUENCE, not of any
// single transition (see [models.ActionRecord]), and three methods would put
// the ordering decision back on the caller that cannot make it. The instant
// because it is the one thing an admin route cannot supply and a carrier
// always can: with it, the [models.ActionRecord] branch could fill in a
// dispatch moment that is TRUE instead of leaving it null, which is the single
// piece of data this module currently has no way to learn.
//
// # Composite data travels as JSON and its schema is declared HERE
//
// The shipping option list does not fit into primitive types; it travels as
// JSON. The field names MUST BE IDENTICAL to the schema on the consumer side,
// and conformance can only be proven by an integration test — because this
// module cannot import the workflow package, the compiler cannot check it.

// CodeInteropRequestInvalid reports that an undecodable request body arrived.
const CodeInteropRequestInvalid = "fulfillment_interop_request_invalid"

// jsonNull is the JSON body that carries no value, read as an empty one.
const jsonNull = "null"

// interopListRequest is the JSON schema of the [Interop.ListOptionsJSON]
// request.
//
//	{
//	  "region_id":             "reg_...",   // the cart's region; may be empty
//	  "currency_code":         "TRY",       // REQUIRED, ISO 4217
//	  "country_code":          "TR",        // delivery country; may be empty
//	  "shipping_profile_ids":  ["sprof_..."], // if empty, no profile filter
//	  "subtotal":              50000,       // minor unit INTEGER
//	  "item_count":            3,
//	  "total_weight":          1500,        // grams
//	  "attributes":            {"customer_group_id": "vip"},
//	  "include_admin_only":    false,       // ONLY admin flows pass true
//	  "is_return":             false
//	}
//
// The numeric fields are INTEGERS and a fractional value is REJECTED. Decoding
// is therefore done with json.Number: the value is first taken as text, then
// converted to int64. A decoding that goes through float64 silently truncates a
// subtotal such as "100.5" to 100 and the money loses a cent (plan Section 8);
// json.Number returns an EXPLICIT error on the same value.
type interopListRequest struct {
	RegionID           string            `json:"region_id"`
	CurrencyCode       string            `json:"currency_code"`
	CountryCode        string            `json:"country_code"`
	ShippingProfileIDs []string          `json:"shipping_profile_ids"`
	Subtotal           json.Number       `json:"subtotal"`
	ItemCount          json.Number       `json:"item_count"`
	TotalWeight        json.Number       `json:"total_weight"`
	Attributes         map[string]string `json:"attributes"`
	IncludeAdminOnly   bool              `json:"include_admin_only"`
	IsReturn           bool              `json:"is_return"`
}

// interopListResponse is the JSON schema of the [Interop.ListOptionsJSON]
// response.
//
//	{
//	  "options": [
//	    {
//	      "id":                  "sopt_...",
//	      "name":                "Standard shipping",
//	      "amount":              2500,        // minor unit INTEGER
//	      "currency_code":       "TRY",
//	      "price_type":          "flat",      // "flat" | "calculated"
//	      "provider_id":         "manual",
//	      "shipping_profile_id": "sprof_...",
//	      "is_return":           false,
//	      "admin_only":          false,
//	      "delivery_days":       {"min": 3, "max": 5}  // absent when the option says none
//	    }
//	  ]
//	}
//
// The list is ordered FIRST by fee (the cheaper one wins) and, on a tie, by
// identifier. The provider's raw data ("data") is NOT CARRIED HERE: it is
// internal data and is not needed for a flow to make a decision.
type interopListResponse struct {
	Options []interopOption `json:"options"`
}

// interopOption is the JSON schema of a single priced shipping option.
type interopOption struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Amount            int64  `json:"amount"`
	CurrencyCode      string `json:"currency_code"`
	PriceType         string `json:"price_type"`
	ProviderID        string `json:"provider_id"`
	ShippingProfileID string `json:"shipping_profile_id"`
	IsReturn          bool   `json:"is_return"`
	AdminOnly         bool   `json:"admin_only"`
	// DeliveryDays is how many business days the option's delivery takes,
	// absent when it says none (ADR 0421).
	DeliveryDays *interopDeliveryDays `json:"delivery_days,omitempty"`
}

// interopDeliveryDays is an option's business days, the least and the most.
type interopDeliveryDays struct {
	Min int32 `json:"min"`
	Max int32 `json:"max"`
}

// toInteropDeliveryDays carries the option's days, nil when it has none.
func toInteropDeliveryDays(days *models.DeliveryDays) *interopDeliveryDays {
	if days == nil {
		return nil
	}
	return &interopDeliveryDays{Min: days.Min, Max: days.Max}
}

// Interop translates the fulfillment service into a PRIMITIVE cross-module
// surface.
//
// It makes no decision at all: it only translates the signature and the JSON
// schema. All business rules stay on [Service]; adding a rule here would mean
// the same rule diverging in two places.
//
// It is registered with the container under the name "fulfillment.interop".
type Interop struct {
	svc *Service
}

// NewInterop builds the cross-module surface for the given service.
func NewInterop(svc *Service) *Interop { return &Interop{svc: svc} }

// ListOptionsJSON returns the shipping options eligible for a cart context
// along with their prices.
//
// The request and response schemas are written out EXPLICITLY in the
// [interopListRequest] and [interopListResponse] documentation.
//
// For "calculated" options the provider is asked for the price; if a provider
// is unreachable, ONLY that option drops out of the list and the call does not
// return an error (rationale: [Service.ListShippingOptionsFor]).
//
// # Cart facts are considered TRUSTED
//
// This surface is IN-PROCESS and is only resolved by the cart/order flows; the
// subtotal, item count and weight reach them from the cart's own record, not
// from an external request. The call is therefore made with TrustedFacts=true
// and rule-bound options (e.g. "free shipping over 500 TRY") are listed here —
// this is the only way they can be shown to the customer; the HTTP storefront
// endpoint never shows them because it cannot verify the facts.
//
// The flag is DELIBERATELY absent from the schema: letting a caller say "trust
// this data" about its own context would be handing validation back to the
// caller.
//
// The counterpart on the consumer side:
//
//	type ShippingOptionLister interface {
//	    ListOptionsJSON(ctx context.Context, request json.RawMessage) (json.RawMessage, error)
//	}
func (i *Interop) ListOptionsJSON(ctx context.Context, request json.RawMessage) (json.RawMessage, error) {
	req, err := decodeListRequest(request)
	if err != nil {
		return nil, err
	}

	subtotal, err := interopInt(req.Subtotal, "subtotal")
	if err != nil {
		return nil, err
	}
	itemCount, err := interopInt(req.ItemCount, "item_count")
	if err != nil {
		return nil, err
	}
	totalWeight, err := interopInt(req.TotalWeight, "total_weight")
	if err != nil {
		return nil, err
	}

	quoted, err := i.svc.ListShippingOptionsFor(ctx, ListOptionsInput{
		RegionID:           req.RegionID,
		CurrencyCode:       req.CurrencyCode,
		CountryCode:        req.CountryCode,
		ShippingProfileIDs: req.ShippingProfileIDs,
		Subtotal:           subtotal,
		ItemCount:          itemCount,
		TotalWeight:        totalWeight,
		Attributes:         req.Attributes,
		TrustedFacts:       true,
		IncludeAdminOnly:   req.IncludeAdminOnly,
		IsReturn:           req.IsReturn,
	})
	if err != nil {
		return nil, err
	}

	out := interopListResponse{Options: make([]interopOption, 0, len(quoted))}
	// The slice is walked BY INDEX: walking by value would copy the whole option
	// on every iteration, and this path runs every time the cart is updated.
	for idx := range quoted {
		out.Options = append(out.Options, interopOption{
			ID:                quoted[idx].Option.ID,
			Name:              quoted[idx].Option.Name,
			Amount:            quoted[idx].Amount,
			CurrencyCode:      quoted[idx].CurrencyCode,
			PriceType:         quoted[idx].Option.PriceType.String(),
			ProviderID:        quoted[idx].Option.ProviderID,
			ShippingProfileID: quoted[idx].Option.ShippingProfileID,
			IsReturn:          quoted[idx].Option.IsReturn,
			AdminOnly:         quoted[idx].Option.AdminOnly,
			DeliveryDays:      toInteropDeliveryDays(quoted[idx].Option.DeliveryDays),
		})
	}

	body, err := json.Marshal(out)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, CodeInteropRequestInvalid,
			"the shipping option list could not be encoded")
	}
	return body, nil
}

// RankLocations orders the candidates by PREFERENCE: the fulfillment leaves
// from the first one.
//
// The candidates are the inventory module's answer to "the locations that have
// enough stock for this item in this quantity"; which one it ships from is a
// SHIPPING decision, and that is why it is here. The rule and its rationale
// (elimination, ordering, what determinism covers and what the policy does NOT
// guarantee) are in the [Service.RankLocations] documentation.
//
// destinationRegionID is the shipping region the fulfillment is going to and is
// REQUIRED: whether the warehouse serves that region is an input to the
// elimination. If it is empty, errors.Invalid is returned.
//
// The returned slice is a SUBSET of the given candidates and its elements are
// EXACTLY the same strings; the caller can look the result up in its own
// candidate ledger.
//
// An empty candidate list returns errors.Conflict and the caller must handle it
// in the SAME branch as insufficient stock; if ALL candidates are eliminated,
// Conflict is likewise returned (with a separate code:
// [CodeNoServiceableLocation]). A candidate list carrying an empty identifier is
// errors.Invalid.
//
// The counterpart on the consumer side:
//
//	type LocationRanker interface {
//	    RankLocations(ctx context.Context, destinationRegionID string, candidateLocationIDs []string) ([]string, error)
//	}
func (i *Interop) RankLocations(
	ctx context.Context,
	destinationRegionID string,
	candidateLocationIDs []string,
) ([]string, error) {
	return i.svc.RankLocations(ctx, destinationRegionID, candidateLocationIDs)
}

// CreateFulfillment opens a fulfillment for an order and returns the
// fulfillment's IDENTIFIER.
//
// reference is the order's identifier; this module does not validate it
// (Principle 2.2 — the link is established through Module Links).
//
// A second call with the same idempotencyKey does not open a NEW fulfillment,
// it returns the existing fulfillment's identifier; that is what keeps a SECOND
// SHIPPING LABEL from being printed when a saga retries a step. If the key is
// the same but the reference or the option differs, errors.Conflict is returned.
//
// It opens a parcel that holds no order line: an after-sale replacement's,
// whose goods are not the order's lines. A parcel an operator opens for the
// order's own goods holds them, and goes through [Interop.CreateFulfillmentHolding]
// (ADR 0409).
//
// It carries no return either, so it opens outgoing parcels only: a return
// option answers errors.Invalid with [CodeOptionDirectionMismatch], and a
// parcel bringing a return back is opened through the admin API (ADR 0384).
//
// destination is the order's shipping address as JSON, in the schema of
// [interopDestination], or empty / "null" when the order has none. It is handed
// to the provider and not stored here (ADR 0194).
//
// The counterpart on the consumer side:
//
//	type FulfillmentCreator interface {
//	    CreateFulfillment(ctx context.Context, reference, optionID, idempotencyKey string,
//	        destination json.RawMessage) (string, error)
//	}
func (i *Interop) CreateFulfillment(
	ctx context.Context,
	reference, optionID, idempotencyKey string,
	destination json.RawMessage,
) (string, error) {
	address, err := decodeDestination(destination)
	if err != nil {
		return "", err
	}

	ful, err := i.svc.CreateFulfillment(ctx, CreateFulfillmentInput{
		Reference:        reference,
		ShippingOptionID: optionID,
		IdempotencyKey:   idempotencyKey,
		Destination:      address,
	})
	if err != nil {
		return "", err
	}
	return ful.ID, nil
}

// interopItem is one item of [Interop.CreateFulfillmentHolding]'s list.
type interopItem struct {
	LineItemID string `json:"line_item_id"`
	Quantity   int64  `json:"quantity"`
}

// CreateFulfillmentHolding opens a parcel for an order's goods and returns its
// identifier (ADR 0409).
//
// items is [{"line_item_id","quantity"}], the units the parcel holds; empty or
// "null" asks for every line's units the order still owes to a parcel, which
// this module counts under the order's lock on a first open and refuses with
// [CodeNothingOwed] when there are none. Everything else is
// [Interop.CreateFulfillment]'s: the same key returns the same parcel, and a
// return option is refused.
//
// The counterpart on the consumer side:
//
//	type FulfillmentHolder interface {
//	    CreateFulfillmentHolding(ctx context.Context, reference, optionID, idempotencyKey string,
//	        destination, items json.RawMessage) (string, error)
//	}
func (i *Interop) CreateFulfillmentHolding(
	ctx context.Context,
	reference, optionID, idempotencyKey string,
	destination, items json.RawMessage,
) (string, error) {
	address, err := decodeDestination(destination)
	if err != nil {
		return "", err
	}

	var listed []interopItem
	if len(items) > 0 && string(items) != jsonNull {
		if err := json.Unmarshal(items, &listed); err != nil {
			return "", errors.Invalid(CodeInvalidInput, "the parcel's items could not be read: %v", err)
		}
	}
	in := CreateFulfillmentInput{
		Reference:        reference,
		ShippingOptionID: optionID,
		IdempotencyKey:   idempotencyKey,
		Destination:      address,
		// Read only when the list is empty: a parcel of the order's goods that
		// names nothing asks for what the order still owes.
		ItemsOwed: true,
	}
	for _, item := range listed {
		in.Items = append(in.Items, FulfillmentItemInput(item))
	}

	ful, err := i.svc.CreateFulfillment(ctx, in)
	if err != nil {
		return "", err
	}

	return ful.ID, nil
}

// interopDestination is the schema of a parcel's destination on the wire; the
// names are the order module's, which sends it.
type interopDestination struct {
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Company   string `json:"company"`
	Address1  string `json:"address_1"`
	Address2  string `json:"address_2"`
	City      string `json:"city"`
	// Province is the unit under the country, an il in Turkey, not the
	// district (ADR 0067).
	Province    string         `json:"province"`
	PostalCode  string         `json:"postal_code"`
	CountryCode string         `json:"country_code"`
	Phone       string         `json:"phone"`
	Metadata    map[string]any `json:"metadata"`
}

// decodeDestination reads the destination; nil when there is none.
//
// Unknown fields are refused rather than dropped: a field the sender added and
// this side does not know would otherwise leave the carrier's label without
// it, and nobody would see that happen.
func decodeDestination(raw json.RawMessage) (*coreprovider.Address, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte(jsonNull)) {
		return nil, nil
	}

	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	var in interopDestination
	if err := decoder.Decode(&in); err != nil {
		return nil, errors.Wrap(err, errors.KindInvalid, CodeInteropRequestInvalid,
			"the parcel's destination could not be read")
	}

	return &coreprovider.Address{
		FirstName:   in.FirstName,
		LastName:    in.LastName,
		Company:     in.Company,
		Address1:    in.Address1,
		Address2:    in.Address2,
		City:        in.City,
		Province:    in.Province,
		PostalCode:  in.PostalCode,
		CountryCode: in.CountryCode,
		Phone:       in.Phone,
		Metadata:    in.Metadata,
	}, nil
}

// CancelFulfillment cancels the fulfillment; this IS THE SAGA COMPENSATION and
// it is IDEMPOTENT.
//
// If it is called twice the second call does NOT return an error. An unknown
// fulfillment identifier, however, returns errors.NotFound; compensation does
// not silently swallow a record that does not exist. A DELIVERED fulfillment
// cannot be canceled and errors.Conflict is returned (rationale:
// [Service.CancelFulfillment]).
//
// The counterpart on the consumer side:
//
//	type FulfillmentCanceler interface {
//	    CancelFulfillment(ctx context.Context, fulfillmentID string) error
//	}
func (i *Interop) CancelFulfillment(ctx context.Context, fulfillmentID string) error {
	return i.svc.CancelFulfillment(ctx, fulfillmentID)
}

// FulfillmentStatus returns the fulfillment's current status ("pending",
// "shipped", "delivered" or "canceled").
//
// The tests that verify the compensation really runs look at this: a canceled
// fulfillment returns "canceled" and the saga's rollback chain becomes visible.
//
// The counterpart on the consumer side:
//
//	type FulfillmentStatusReader interface {
//	    FulfillmentStatus(ctx context.Context, fulfillmentID string) (string, error)
//	}
func (i *Interop) FulfillmentStatus(ctx context.Context, fulfillmentID string) (string, error) {
	ful, err := i.svc.GetFulfillment(ctx, fulfillmentID)
	if err != nil {
		return "", err
	}
	return ful.Status.String(), nil
}

// decodeListRequest decodes the raw request body.
//
// Numbers are decoded as json.Number: the value is first taken as text and,
// while [interopInt] converts it to int64, it rejects a fractional body with an
// EXPLICIT error. A decoding that goes through float64 would silently truncate
// the same body, and the subtotal is a MONEY value (plan Section 8).
func decodeListRequest(raw json.RawMessage) (interopListRequest, error) {
	if len(raw) == 0 || string(raw) == jsonNull {
		return interopListRequest{}, errors.Invalid(CodeInteropRequestInvalid,
			"the shipping option request cannot be empty")
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	// An unrecognized field is REJECTED: a silently swallowed field means a
	// condition the caller believes it sent but which is not applied, and it is
	// the first sign that the schema in the two packages has diverged.
	dec.DisallowUnknownFields()

	var out interopListRequest
	if err := dec.Decode(&out); err != nil {
		return interopListRequest{}, errors.Wrap(err, errors.KindInvalid, CodeInteropRequestInvalid,
			"the shipping option request could not be decoded; it must be a JSON object")
	}
	return out, nil
}

// interopInt converts a json.Number to int64; an empty value returns zero.
func interopInt(value json.Number, field string) (int64, error) {
	if value == "" {
		return 0, nil
	}
	parsed, err := value.Int64()
	if err != nil {
		return 0, errors.Wrap(err, errors.KindInvalid, CodeInteropRequestInvalid,
			"%s has to be an integer: %q", field, value.String())
	}
	return parsed, nil
}

// CommittedQuantitiesForReference sums, per order line, the units the outgoing
// parcels hold for the reference, an addition's in its parent's parcel among
// them (ADR 0428), as the module counts them when it
// holds a new parcel to its order (ADR 0409). The fulfilling flow answers what
// an order still owes a parcel from it, so the panel's form offers what this
// module will take.
//
// spoken is, per line, how many units a return or a replacement speaks for: a
// parcel that came back undelivered holds its units only so far, and the rest
// are owed again (ADR 0423).
func (i *Interop) CommittedQuantitiesForReference(
	ctx context.Context, reference string, spoken map[string]int64,
) (map[string]int64, error) {
	return i.svc.CommittedQuantitiesForReference(ctx, reference, spoken)
}

// HeldForReferenceLocked sums, per order line, the units the outgoing parcels
// hold for the reference, an addition's in its parent's parcel among them (ADR
// 0428), read under the order's dispatch lock (ADR 0420). A cancellation asks
// it before putting stock back. It does not wait for the lock: while a parcel
// of the order is being opened or joined it answers [errors.KindUnavailable]
// with [CodeDispatchBusy], and asked again after the open commits it counts
// that parcel. It counts a parcel whose link to the order was not written.
// spoken is read as [Interop.CommittedQuantitiesForReference] reads it (ADR
// 0423).
//
// The counterpart on the consumer side:
//
//	type Fulfillment interface {
//	    HeldForReferenceLocked(ctx context.Context, reference string, spoken map[string]int64) (map[string]int64, error)
//	}
func (i *Interop) HeldForReferenceLocked(
	ctx context.Context, reference string, spoken map[string]int64,
) (map[string]int64, error) {
	return i.svc.HeldForReferenceLocked(ctx, reference, spoken)
}

// JoinParcel puts units order additionReference still owes into pending
// parcel fulfillmentID of order parentReference, as items the addition owns
// (ADR 0428). The rules are [Service.JoinParcel]'s.
//
// items is [Interop.CreateFulfillmentHolding]'s list, each unit held to what
// its line still owes; empty or "null" asks for every unit the addition still
// owes, which itemsOwed allows only for an addition sold exactly one delivery
// on a shipping option, and is refused with [CodeItemsRequired] otherwise. A
// parcel already holding the addition's items is answered as joined, in any
// state and whatever items names. The caller binds the addition to the parcel
// through the "order_fulfillment" link once it returns, as the fulfilling
// flow does (ADR 0197).
//
// The counterpart on the consumer side:
//
//	type ParcelJoiner interface {
//	    JoinParcel(ctx context.Context, fulfillmentID, parentReference, additionReference string,
//	        items json.RawMessage, itemsOwed bool) error
//	}
func (i *Interop) JoinParcel(
	ctx context.Context,
	fulfillmentID, parentReference, additionReference string,
	items json.RawMessage, itemsOwed bool,
) error {
	in := JoinParcelInput{
		FulfillmentID:     fulfillmentID,
		ParentReference:   parentReference,
		AdditionReference: additionReference,
		ItemsOwed:         itemsOwed,
	}
	if len(items) > 0 && string(items) != jsonNull {
		var listed []interopItem
		if err := json.Unmarshal(items, &listed); err != nil {
			return errors.Invalid(CodeInvalidInput, "the items joining the parcel could not be read: %v", err)
		}
		for _, item := range listed {
			in.Items = append(in.Items, FulfillmentItemInput(item))
		}
	}

	_, err := i.svc.JoinParcel(ctx, in)

	return err
}

// CommittedQuantities sums, per order line, the units a live outgoing parcel
// holds.
//
// # Why a cancellation needs it
//
// The checkout deducts stock, so units written off afterwards are units nobody
// will send and nobody counts as stock either. Which of them can go back on the
// shelf depends on whether they already left: this answers that, per line, for a
// set of parcels (ADR 0134).
//
// # What counts as gone
//
// Everything but a CANCELED parcel. A canceled one's goods never left the
// building; a shipped or delivered one's did. A PENDING parcel counts as gone
// because the warehouse is already picking it, and treating those units as
// available would let a cancellation put back goods that are in a box. A parcel
// bringing a return back never counts, whatever list names it: its units never
// left with the order's goods (ADR 0384).
//
// A RETURNED parcel counts whole here. This count's one reader in this tree is
// the backorder claim at the checkout, which a recovery can run after the
// order's parcels exist (gaps D268); the counts by reference that open a parcel
// and restock a write-off hold one that came back undelivered only as far as
// a return or a replacement speaks for its units ([Interop.HeldForReferenceLocked],
// ADR 0423).
//
// The parcels are named by the caller rather than looked up from an order,
// because the binding between the two is the "order_fulfillment" LINK and this
// module does not read another module's links.
func (i *Interop) CommittedQuantities(
	ctx context.Context, fulfillmentIDs []string,
) (map[string]int64, error) {
	return i.svc.CommittedQuantities(ctx, fulfillmentIDs)
}

// QuantitiesOfFulfillment sums, per order line, the units ONE parcel holds.
//
// # Why it exists beside CommittedQuantities
//
// The two look alike and answer opposite questions. [Interop.CommittedQuantities]
// skips a CANCELED parcel on purpose, because it is asked "how many units of this
// line have left the building". This one is asked "what was in this box", by a
// subscriber that is looking at the parcel BECAUSE it was just canceled — so the
// status filter that makes the first answer right would make this one empty
// exactly when it matters (ADR 0139).
//
// An unknown parcel answers an empty map. The caller is acting on an event, and a
// parcel that no longer exists is a fact it can do nothing with.
//
// The counterpart on the consumer side:
//
//	type ParcelContents interface {
//	    QuantitiesOfFulfillment(ctx context.Context, fulfillmentID string) (map[string]int64, error)
//	}
func (i *Interop) QuantitiesOfFulfillment(
	ctx context.Context, fulfillmentID string,
) (map[string]int64, error) {
	return i.svc.QuantitiesOfFulfillment(ctx, fulfillmentID)
}
