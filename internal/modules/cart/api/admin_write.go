package api

import (
	"context"
	"net/http"
	"strings"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// This file is the cart module's first admin WRITE surface, and it exists for
// one shop act: taking an order over the telephone.
//
// # Why it goes through the cart and not through the order
//
// `CreateOrder` deliberately has no route. An order opened over HTTP could carry
// a total the caller decided — a total of zero — and the only thing that makes
// the total the server's is the completion flow. So an operator builds a CART,
// which prices every line on the server exactly as the storefront does, and the
// shopper pays it through the endpoint that already exists.
//
// # Why the LINE write names a sales channel, when the storefront never does
//
// The storefront's channel is proven: it comes from the publishable key's record,
// and the cart workflow refuses to take it from the client for the reason written
// in `workflows/cart/saleschannel.go` — a client filling it in itself would make
// the scope its own to choose.
//
// An administrator is not that caller. They hold `cart:write`, the guard ring has
// proved who they are, and the audit ring is recording the request; what they are
// doing is declaring which shopfront this sale belongs to, which a multi-channel
// shop has to be able to say. So the channel is REQUIRED on the line write and
// asserted into the principal the flow reads, which means the cart's scope rule
// runs unchanged rather than being skipped (ADR 0146).
//
// The alternative was to let an admin request carry no channel claim at all. It
// was measured and refused: a principal with no channels is "bound to no channel",
// not "unscoped", so every line write would have answered 404 for any product the
// shop had assigned to a channel — and that 404 is deliberately indistinguishable
// from "no such variant", so the operator would have had nothing to go on.
//
// # Why OPENING a cart names none
//
// Because nothing on that path reads one. The region and the currency are derived
// from the country, the customer comes from the body, and no catalog is touched
// until the first line — so a channel required here would be a claim asserted
// into a context nobody consults, which is the defect this repository keeps
// closing rather than a symmetry worth having. The claim is made where it is
// READ.

// adminCreateCartRequest is the body an operator sends to open a cart.
type adminCreateCartRequest struct {
	// CountryCode decides the region and the currency ON THE SERVER, exactly as
	// on the storefront.
	CountryCode string `json:"country_code"`
	// CustomerID names the person the cart is for; empty opens a guest cart.
	//
	// This is the field the storefront may not be trusted with (ADR 0125): there
	// a body naming a customer has to be proved. Here the caller is an
	// administrator with `cart:write`, which is the whole difference — and the
	// point of the endpoint, because a cart opened in the customer's name carries
	// their history and their company's spending limit.
	CustomerID string `json:"customer_id"`
	// Email is the address the order will be confirmed to; it may be empty.
	Email string `json:"email"`
	// Metadata is the free-form object attached to the cart.
	Metadata map[string]any `json:"metadata"`
}

// adminAddLineItemRequest is the body that writes one line.
type adminAddLineItemRequest struct {
	// SalesChannelID is REQUIRED on every write, and the cart does not remember
	// one.
	//
	// The storefront does not need it because its key carries it on every
	// request; here it is the operator's claim, and a claim is made per request
	// rather than once. What that costs is written in ADR 0146: two lines of one
	// cart can be written under two channels, which is the operator's own doing
	// and which the storefront cannot produce.
	SalesChannelID string `json:"sales_channel_id"`
	// VariantID is the product variant being added.
	VariantID string `json:"variant_id"`
	// Quantity is how many units; it is required and has to be positive.
	Quantity *int64 `json:"quantity"`
	// Metadata is the free-form object attached to the line.
	Metadata map[string]any `json:"metadata"`
}

// adminCreateCart opens a cart an operator is building
// (POST /admin/v1/carts).
//
// It names no sales channel; the reason is at the top of this file.
func (h *Handler) adminCreateCart(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body adminCreateCartRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	flow, err := h.opening()
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	metadata, err := encodeMetadata(body.Metadata)
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	id, err := flow.OpenCartForCountry(ctx, body.CountryCode, body.CustomerID,
		body.Email, metadata)
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	detail, err := h.svc.GetCart(ctx, id)
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	corehttp.WriteJSON(ctx, w, http.StatusCreated,
		singleEnvelope{Data: toCartDetailDTO(detail)})
}

// adminAddLineItem writes one line into a cart an operator is building
// (POST /admin/v1/carts/{id}/line-items).
//
// The price is the SERVER's, the same way it is for a shopper: this endpoint
// takes no amount and no title, because a surface that accepted them would let an
// operator sell at a price nothing in the catalog says.
func (h *Handler) adminAddLineItem(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body adminAddLineItemRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}
	if body.Quantity == nil {
		corehttp.WriteError(ctx, w, coreerrors.Invalid(codeInvalidRequest,
			"quantity is mandatory"))

		return
	}

	scoped, ok := h.channelScoped(ctx, w, body.SalesChannelID)
	if !ok {
		return
	}

	flow, err := h.pricing()
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	metadata, err := encodeMetadata(body.Metadata)
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	id := cartID(r)
	lineID, err := flow.AddPricedLineItem(scoped, id, body.VariantID, *body.Quantity, metadata)
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	item, err := h.lineItem(ctx, id, lineID)
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	corehttp.WriteJSON(ctx, w, http.StatusCreated,
		singleEnvelope{Data: toLineItemDTO(item)})
}

// channelScoped asserts the operator's channel into the request's identity.
//
// It is called from the line write, the only admin write whose flow reads a
// catalog.
//
// # Why it is written into the PRINCIPAL and not passed as a parameter
//
// Because the cart workflow reads the scope from the principal, deliberately: a
// channel carried as a parameter would let a new write path forget to pass it and
// the scope would disappear in silence (`workflows/cart/saleschannel.go`). Writing
// it here means the rule runs unchanged — the flow cannot tell this request from a
// storefront one, and that is the point.
//
// The rest of the identity is carried over untouched, so the audit row still names
// the administrator who made the request.
//
// # An empty channel is a 422 and not a silent 404
//
// A principal with no channels means "bound to no channel", and the catalog answers
// such a request with the products that have NO assignment — so on any shop that
// uses channels the operator would get "that variant is not in the catalog" for
// everything, with no way to tell it from a typo. Refusing here says what is
// missing.
func (h *Handler) channelScoped(
	ctx context.Context, w http.ResponseWriter, channelID string,
) (scoped context.Context, ok bool) {
	if strings.TrimSpace(channelID) == "" {
		corehttp.WriteError(ctx, w, coreerrors.Invalid(codeInvalidRequest,
			"sales_channel_id is mandatory: an administrator's write says which "+
				"shopfront the sale belongs to, and without it the catalog would answer "+
				"with the products assigned to no channel at all"))

		return ctx, false
	}

	principal, found := corehttp.PrincipalFromContext(ctx)
	if !found {
		// The guard ring already refused an unauthenticated request, so reaching
		// here without one means the rings are wired wrongly rather than that the
		// caller did something.
		corehttp.WriteError(ctx, w, coreerrors.Internal(codeInvalidRequest,
			"the caller could not be identified, so no channel claim can be recorded"))

		return ctx, false
	}

	principal.SalesChannelIDs = []string{strings.TrimSpace(channelID)}

	return corehttp.WithPrincipal(ctx, principal), true
}
