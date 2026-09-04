package service

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// This file is the product module's CROSS-MODULE surface (ADR 0001, ADR 0006).
//
// Plugins (plugins/**) and workflows CANNOT import this module. The solution is
// the same as the interop.go of the other modules: to publish a surface that
// uses ONLY PRIMITIVE and stdlib types. The consumer defines its own narrow
// interface, this type satisfies it STRUCTURALLY, and it is resolved from the
// container under the name "product.interop".
//
// The reason is Go's structural conformance rule: since the consumer cannot
// import product, it cannot name a type such as StoreProduct in its signature;
// the moment it names one, that becomes ANOTHER type defined in its own package
// and the concrete service does not satisfy the consumer's interface.
//
// The surface is DELIBERATELY narrow: today all it has is "give me the
// storefront records of these ids". The source of that need is search — a
// search plugin produces ids and a relevance ORDER from its own index, and has
// to read the record it is going to display from the catalog itself. Every
// method added here raises the cost of extracting product into a separate
// service.

// CodeInteropRequestInvalid reports that a request body arrived that cannot be
// decoded.
const CodeInteropRequestInvalid = "product_interop_request_invalid"

// interopStoreProductsRequest is the JSON schema of the
// [Interop.StoreProductsByIDsJSON] request.
//
//	{
//	  "ids":               ["prod_...", "prod_..."],
//	  "sales_channel_ids": ["sc_..."]
//	}
//
// # sales_channel_ids may be absent and that HAS a meaning
//
// The field carries the SAME meaning as in the storefront listing
// (see [StoreListOptions.SalesChannelIDs]) and is NOT REDEFINED here:
//
//   - absent or null: the request carries no sales channel id at all and no
//     filter is applied. This is the counterpart of the setup where store
//     authentication is not wired up at all (see api/store.go salesChannelIDs).
//   - empty array: there is an identity but it has no channel. The filter IS
//     APPLIED and only the products with no assignment are returned.
//
// Reading the distinction any other way here — counting the absent field as "show
// nothing", for instance — would be a SECOND definition of the rule, and two
// definitions drift apart one day. The consumer's responsibility is to carry the
// channels from the IDENTITY of the request; not from a query parameter the user
// sent.
type interopStoreProductsRequest struct {
	IDs             []string `json:"ids"`
	SalesChannelIDs []string `json:"sales_channel_ids"`
}

// interopStoreProductsResponse is the JSON schema of the
// [Interop.StoreProductsByIDsJSON] response.
//
//	{"products": [ <storefront product record>, ... ]}
//
// The shape of the records is [StoreProduct] itself: the SAME type the HTTP
// storefront endpoint writes as its body is serialized, and the fields are NOT
// LISTED AGAIN here. Redefining the fields in this file would produce a second
// copy of the storefront representation, and the two would silently drift apart
// once a field was added.
//
// There is NO pagination envelope (count/offset/limit): the caller already knows
// which ids it asked for and does the pagination in its own index.
type interopStoreProductsResponse struct {
	Products []StoreProduct `json:"products"`
}

// Interop turns the product service into the CROSS-MODULE PRIMITIVE surface.
//
// It makes no decisions: it only translates the signature and the JSON schema.
// Every rule, visibility included, stays on [Service]; adding a rule here would
// mean the same rule drifting apart in two places — and on this surface a
// drifting rule would mean search skipping the channel filter.
//
// It is registered in the container under the name "product.interop".
type Interop struct {
	svc *Service
}

// NewInterop builds the cross-module surface for the given service.
func NewInterop(svc *Service) *Interop { return &Interop{svc: svc} }

// StoreProductsByIDsJSON returns the STOREFRONT records of the given ids.
//
// The request and response schemas are written out EXPLICITLY in the
// [interopStoreProductsRequest] and [interopStoreProductsResponse] docs.
//
// The records preserve the id ORDER of the request; an id that is not found, is
// not published or is not visible in the sales channels of the request is
// silently skipped. The whole rule and its rationale are in the
// [Service.StoreProductsByIDs] doc.
//
// An empty "ids" is NOT an error: an empty product list is returned. That is the
// contract of bulk read surfaces (ADR 0004) and "no ids at all" and "the ids are
// invalid" are different things; the second returns errors.Invalid.
//
// The counterpart on the consumer side:
//
//	type StoreProductReader interface {
//	    StoreProductsByIDsJSON(ctx context.Context, request json.RawMessage) (json.RawMessage, error)
//	}
func (i *Interop) StoreProductsByIDsJSON(ctx context.Context, request json.RawMessage) (json.RawMessage, error) {
	req, err := decodeStoreProductsRequest(request)
	if err != nil {
		return nil, err
	}

	products, err := i.svc.StoreProductsByIDs(ctx, req.IDs, req.SalesChannelIDs)
	if err != nil {
		return nil, err
	}

	body, err := json.Marshal(interopStoreProductsResponse{Products: products})
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, CodeInteropRequestInvalid,
			"the storefront product list could not be encoded (%d products)", len(products))
	}
	return body, nil
}

// decodeStoreProductsRequest decodes the raw request body.
//
// The decoding is done with json.Decoder because the distinction between a nil
// slice and an EMPTY slice has to be preserved: when "sales_channel_ids" is
// absent at all the field stays nil and no filter is applied, while an empty
// array produces a NON-nil empty slice and the filter is applied (see
// [interopStoreProductsRequest]).
func decodeStoreProductsRequest(raw json.RawMessage) (interopStoreProductsRequest, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return interopStoreProductsRequest{}, errors.Invalid(CodeInteropRequestInvalid,
			"the storefront product request cannot be empty")
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	// An unrecognized field is REJECTED: a silently swallowed field means a
	// condition the caller believes it sent but that is not applied. Here that
	// condition is most likely the channel filter (a consumer writing "channel_ids"
	// would read the whole published catalog while believing it had applied a
	// filter).
	dec.DisallowUnknownFields()

	var out interopStoreProductsRequest
	if err := dec.Decode(&out); err != nil {
		return interopStoreProductsRequest{}, errors.Wrap(err, errors.KindInvalid, CodeInteropRequestInvalid,
			"the storefront product request could not be decoded; it has to be a JSON object")
	}
	return out, nil
}
