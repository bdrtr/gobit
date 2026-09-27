package api

import (
	"net/http"
	"strconv"
	"strings"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// pathStoreFacets is the channel-scoped facet count (ADR 0219).
const pathStoreFacets = "/store/v1/sales-channels/{sales_channel_id}/product-facets"

// attributeRequest defines a store-wide attribute.
type attributeRequest struct {
	// Handle is how the storefront names it; derived from the title when empty.
	Handle string `json:"handle"`
	Title  string `json:"title"`
	// Kind is number, boolean or select.
	Kind string `json:"kind"`
	Rank int32  `json:"rank"`
	// Options are a select attribute's choices.
	Options []attributeOptionRequest `json:"options"`
}

// attributeOptionRequest is one choice of a select attribute.
type attributeOptionRequest struct {
	// Handle is how the storefront names it; derived from the value when empty.
	Handle string `json:"handle"`
	Value  string `json:"value"`
	Rank   int32  `json:"rank"`
}

// attributePatchRequest changes a definition's title or order.
type attributePatchRequest struct {
	Title *string `json:"title"`
	Rank  *int32  `json:"rank"`
}

// productAttributesRequest replaces a product's attribute values.
type productAttributesRequest struct {
	Values []productAttributeValueRequest `json:"values"`
}

// productAttributeValueRequest is a product's value of one attribute: the
// option handles of a select attribute, a number, or a boolean.
type productAttributeValueRequest struct {
	Attribute string   `json:"attribute"`
	Options   []string `json:"options"`
	Number    *float64 `json:"number"`
	Boolean   *bool    `json:"boolean"`
}

// facetsResponse is the facet count's body.
type facetsResponse struct {
	Data []service.Facet `json:"data"`
}

// adminCreateAttribute POST /admin/v1/product-attributes
func (h *Handler) adminCreateAttribute(w http.ResponseWriter, r *http.Request) {
	req, err := decode[attributeRequest](w, r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)

		return
	}
	in := service.AttributeInput{Handle: req.Handle, Title: req.Title, Kind: req.Kind, Rank: req.Rank}
	for _, o := range req.Options {
		in.Options = append(in.Options, service.AttributeOptionInput{Handle: o.Handle, Value: o.Value, Rank: o.Rank})
	}
	attribute, err := h.svc.CreateAttribute(r.Context(), in)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)

		return
	}
	writeItem(w, r, http.StatusCreated, attribute)
}

// adminListAttributes GET /admin/v1/product-attributes
func (h *Handler) adminListAttributes(w http.ResponseWriter, r *http.Request) {
	h.writeAttributes(w, r)
}

// storeListAttributes GET /store/v1/product-attributes
//
// The vocabulary of the attribute filter: every definition and its options,
// in the operator's order. Like the tags, it is not channel-scoped.
func (h *Handler) storeListAttributes(w http.ResponseWriter, r *http.Request) {
	h.writeAttributes(w, r)
}

// writeAttributes writes every definition as a list; there is no paging, since
// a catalog defines at most models.MaxAttributes.
func (h *Handler) writeAttributes(w http.ResponseWriter, r *http.Request) {
	attributes, err := h.svc.ListAttributes(r.Context())
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)

		return
	}
	count := len(attributes)
	writeList(w, r, service.ListResult[models.Attribute]{Items: attributes, Count: &count, Limit: count})
}

// adminUpdateAttribute PATCH /admin/v1/product-attributes/{id}
func (h *Handler) adminUpdateAttribute(w http.ResponseWriter, r *http.Request) {
	id, err := pathParam(r, "id")
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)

		return
	}
	req, err := decode[attributePatchRequest](w, r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)

		return
	}
	attribute, err := h.svc.UpdateAttribute(r.Context(), id, service.AttributePatch{Title: req.Title, Rank: req.Rank})
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)

		return
	}
	writeItem(w, r, http.StatusOK, attribute)
}

// adminDeleteAttribute DELETE /admin/v1/product-attributes/{id}
func (h *Handler) adminDeleteAttribute(w http.ResponseWriter, r *http.Request) {
	id, err := pathParam(r, "id")
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)

		return
	}
	if err := h.svc.DeleteAttribute(r.Context(), id); err != nil {
		corehttp.WriteError(r.Context(), w, err)

		return
	}
	writeItem(w, r, http.StatusOK, deleted{ID: id, Object: "product_attribute", Deleted: true})
}

// adminAddAttributeOption POST /admin/v1/product-attributes/{id}/options
func (h *Handler) adminAddAttributeOption(w http.ResponseWriter, r *http.Request) {
	id, err := pathParam(r, "id")
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)

		return
	}
	req, err := decode[attributeOptionRequest](w, r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)

		return
	}
	option, err := h.svc.AddAttributeOption(r.Context(), id, service.AttributeOptionInput{
		Handle: req.Handle, Value: req.Value, Rank: req.Rank,
	})
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)

		return
	}
	writeItem(w, r, http.StatusCreated, option)
}

// adminDeleteAttributeOption DELETE /admin/v1/product-attribute-options/{id}
func (h *Handler) adminDeleteAttributeOption(w http.ResponseWriter, r *http.Request) {
	id, err := pathParam(r, "id")
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)

		return
	}
	if err := h.svc.DeleteAttributeOption(r.Context(), id); err != nil {
		corehttp.WriteError(r.Context(), w, err)

		return
	}
	writeItem(w, r, http.StatusOK, deleted{ID: id, Object: "product_attribute_option", Deleted: true})
}

// adminSetProductAttributes PUT /admin/v1/products/{id}/attributes
//
// The body replaces the product's attribute values whole; an empty list
// clears them.
func (h *Handler) adminSetProductAttributes(w http.ResponseWriter, r *http.Request) {
	id, err := pathParam(r, "id")
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)

		return
	}
	req, err := decode[productAttributesRequest](w, r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)

		return
	}
	values := make([]service.ProductAttributeInput, 0, len(req.Values))
	for _, v := range req.Values {
		values = append(values, service.ProductAttributeInput{
			Attribute: v.Attribute, Options: v.Options, Number: v.Number, Boolean: v.Boolean,
		})
	}
	stored, err := h.svc.SetProductAttributes(r.Context(), id, values)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)

		return
	}
	if stored == nil {
		stored = []models.ProductAttributeValue{}
	}
	writeItem(w, r, http.StatusOK, stored)
}

// storeFacets GET /store/v1/sales-channels/{sales_channel_id}/product-facets
//
// It takes the listing's catalog filters, attributes included, and answers
// how many of the products they keep hold each value of every attribute. The
// body is a function of the URL alone, like the listing's.
func (h *Handler) storeFacets(w http.ResponseWriter, r *http.Request) {
	channels, err := storeChannelScope(r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)

		return
	}
	attributes, err := attributesParam(r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)

		return
	}
	inStock, err := optionalBoolParam(r, "in_stock")
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)

		return
	}
	bracket, err := priceBracketParam(r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)

		return
	}
	facets, err := h.svc.StoreFacets(r.Context(), service.StoreListOptions{
		CollectionID:    stringParam(r, "collection_id"),
		CategoryID:      stringParam(r, "category_id"),
		TagID:           stringParam(r, "tag_id"),
		OptionValue:     stringParam(r, "option_value"),
		VariantIDs:      variantIDsParam(r),
		Attributes:      attributes,
		Search:          stringParam(r, "q"),
		InStock:         inStock,
		Price:           bracket,
		SalesChannelIDs: channels,
	})
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)

		return
	}
	if facets == nil {
		facets = []service.Facet{}
	}
	h.allowCaching(w)
	corehttp.WriteJSON(r.Context(), w, http.StatusOK, facetsResponse{Data: facets})
}

// attributesParam reads the repeated "attribute" query parameter (ADR 0219).
//
// Each one is "<handle>:<value>": option handles separated by commas for a
// select attribute ("material:cotton,wool"), "true" or "false" for a boolean
// one, and "<min>..<max>" for a number, either side left out for an open end
// ("width:..120"). Which of them a value is, is the attribute's kind's to
// decide, so the service reads it; an option handle cannot hold "..", and a
// select option called "true" is read as that option.
func attributesParam(r *http.Request) ([]service.AttributeCriterion, error) {
	raw, ok := r.URL.Query()["attribute"]
	if !ok {
		return nil, nil
	}
	out := make([]service.AttributeCriterion, 0, len(raw))
	for _, item := range raw {
		handle, value, found := strings.Cut(item, ":")
		if !found || strings.TrimSpace(handle) == "" || strings.TrimSpace(value) == "" {
			return nil, coreerrors.Invalid(codeBadParam,
				"attribute takes \"<handle>:<value>\", as in material:cotton,wool, width:10..120 or waterproof:true; %q is not one", item)
		}
		c := service.AttributeCriterion{Attribute: strings.TrimSpace(handle)}
		if low, high, isRange := strings.Cut(value, ".."); isRange {
			bound := func(text string) (*float64, error) {
				text = strings.TrimSpace(text)
				if text == "" {
					return nil, nil
				}
				n, err := strconv.ParseFloat(text, 64)
				if err != nil {
					return nil, coreerrors.Invalid(codeBadParam, "%q in attribute %q is no number", text, item)
				}
				return &n, nil
			}
			var err error
			if c.Min, err = bound(low); err != nil {
				return nil, err
			}
			if c.Max, err = bound(high); err != nil {
				return nil, err
			}
		} else {
			for _, v := range strings.Split(value, ",") {
				if v = strings.TrimSpace(v); v != "" {
					c.Options = append(c.Options, v)
				}
			}
		}
		out = append(out, c)
	}
	return out, nil
}
