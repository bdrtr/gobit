package api

import (
	"time"

	"github.com/bdrtr/gobit/internal/modules/pricing/models"
	"github.com/bdrtr/gobit/internal/modules/pricing/service"
)

// The DTOs are kept SEPARATE from the domain models: JSON field names are the
// outside contract and a rename made in the model must not break the client.

// priceSetDTO is the response body of a price set.
type priceSetDTO struct {
	// ID is the container's id.
	ID string `json:"id"`
	// Prices are the container's prices; nil when not requested (absent in JSON).
	Prices []priceDTO `json:"prices,omitempty"`
	// CreatedAt is the moment of creation (RFC3339, UTC).
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is the moment of the last update (RFC3339, UTC).
	UpdatedAt time.Time `json:"updated_at"`
}

// priceDTO is the response body of a price.
type priceDTO struct {
	// ID is the price's id.
	ID string `json:"id"`
	// PriceSetID is the container the price belongs to.
	PriceSetID string `json:"price_set_id"`
	// PriceListID is the list the price is bound to; null for a base price.
	PriceListID *string `json:"price_list_id"`
	// CurrencyCode is the ISO 4217 code (UPPERCASE).
	CurrencyCode string `json:"currency_code"`
	// Amount is the amount in minor units.
	Amount int64 `json:"amount"`
	// MinQuantity is the lower quantity bound.
	MinQuantity int32 `json:"min_quantity"`
	// MaxQuantity is the upper quantity bound; null when unbounded.
	MaxQuantity *int32 `json:"max_quantity"`
	// Rules are the price's validity conditions.
	Rules []priceRuleDTO `json:"rules"`
	// CreatedAt is the moment of creation.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is the moment of the last update.
	UpdatedAt time.Time `json:"updated_at"`
}

// priceRuleDTO is the response body of a price rule.
type priceRuleDTO struct {
	// ID is the rule's id.
	ID string `json:"id"`
	// PriceID is the price the rule is bound to.
	PriceID string `json:"price_id"`
	// Attribute is the name of the field looked at in the calculation context.
	Attribute string `json:"attribute"`
	// Operator is the comparison operator.
	Operator string `json:"operator"`
	// Values is the right-hand side of the comparison.
	Values []string `json:"values"`
	// CreatedAt is the moment of creation.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is the moment of the last update.
	UpdatedAt time.Time `json:"updated_at"`
}

// priceListDTO is the response body of a price list.
type priceListDTO struct {
	// ID is the list's id.
	ID string `json:"id"`
	// Title is the list's display name.
	Title string `json:"title"`
	// Description is the description.
	Description string `json:"description"`
	// Type is the list's type (sale | override).
	Type string `json:"type"`
	// Status is the list's status (draft | active | expired).
	Status string `json:"status"`
	// StartsAt is the start of the validity window; null when absent.
	StartsAt *time.Time `json:"starts_at"`
	// EndsAt is the end of the validity window; null when absent.
	EndsAt *time.Time `json:"ends_at"`
	// CreatedAt is the moment of creation.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is the moment of the last update.
	UpdatedAt time.Time `json:"updated_at"`
}

// calculatedPriceDTO is the response body of a calculation result.
type calculatedPriceDTO struct {
	// PriceID is the id of the selected price.
	PriceID string `json:"price_id"`
	// PriceSetID is the container's id.
	PriceSetID string `json:"price_set_id"`
	// CurrencyCode is the currency of the selected price.
	CurrencyCode string `json:"currency_code"`
	// Amount is the minor unit amount per unit.
	Amount int64 `json:"amount"`
	// Quantity is the quantity the calculation was made for.
	Quantity int32 `json:"quantity"`
	// Total is the amount × quantity product.
	Total int64 `json:"total"`
	// MinQuantity is the lower quantity bound of the selected price.
	MinQuantity int32 `json:"min_quantity"`
	// MaxQuantity is the upper quantity bound of the selected price; null when
	// unbounded.
	MaxQuantity *int32 `json:"max_quantity"`
	// PriceListID is the list's id if the price comes from a list.
	PriceListID *string `json:"price_list_id"`
	// PriceListType is the list's type if the price comes from a list.
	PriceListType *string `json:"price_list_type"`
	// MatchedRules is the number of matched rules of the selected price; it
	// explains WHY the selection fell to that price.
	MatchedRules int `json:"matched_rules"`
}

// priceRequest is the request body of a single price.
type priceRequest struct {
	// CurrencyCode is the ISO 4217 code; the case is free.
	CurrencyCode string `json:"currency_code"`
	// Amount is the amount in minor units.
	Amount int64 `json:"amount"`
	// MinQuantity is the lower quantity bound; if 0 is given it is taken as 1.
	MinQuantity int32 `json:"min_quantity"`
	// MaxQuantity is the upper quantity bound; if null it is unbounded.
	MaxQuantity *int32 `json:"max_quantity"`
	// PriceListID binds the price to a list; if null it is a base price.
	PriceListID *string `json:"price_list_id"`
	// Rules are the price's validity conditions.
	Rules []ruleRequest `json:"rules"`
}

// ruleRequest is the request body of a single price rule.
type ruleRequest struct {
	// Attribute is the name of the field to look at in the calculation context.
	Attribute string `json:"attribute"`
	// Operator is the comparison operator (eq|ne|in|nin|gt|gte|lt|lte).
	Operator string `json:"operator"`
	// Values is the right-hand side of the comparison.
	Values []string `json:"values"`
}

// createPriceSetRequest is the price set creation request.
type createPriceSetRequest struct {
	// Prices are the prices to be written along with the container; it may be left
	// empty.
	Prices []priceRequest `json:"prices"`
}

// setPricesRequest is the request to write a container's prices in bulk.
type setPricesRequest struct {
	// Prices is the container's NEW price set; the ones not given are deleted.
	Prices []priceRequest `json:"prices"`
}

// priceListRequest is the price list creation/update request.
type priceListRequest struct {
	// Title is the list's display name; it is required.
	Title string `json:"title"`
	// Description is the description.
	Description string `json:"description"`
	// Type is the list's type (sale | override); it is required.
	Type string `json:"type"`
	// Status is the list's status; draft when empty.
	Status string `json:"status"`
	// StartsAt is the start of the validity window.
	StartsAt *time.Time `json:"starts_at"`
	// EndsAt is the end of the validity window.
	EndsAt *time.Time `json:"ends_at"`
}

// The calculation request has NO body counterpart: the endpoint is a GET and
// reads its context from the query string (see [API.calculatePrice],
// [calculateQuery]).

// toPriceSetDTO converts the price set model into the response body.
// If prices is given as nil the price field is not written into the response.
func toPriceSetDTO(set models.PriceSet, prices []models.Price) priceSetDTO {
	dto := priceSetDTO{
		ID:        set.ID,
		CreatedAt: set.CreatedAt,
		UpdatedAt: set.UpdatedAt,
	}
	if prices != nil {
		dto.Prices = toPriceDTOs(prices)
	}
	return dto
}

// toPriceSetSummaryDTO produces a price set body without prices; it is used for
// list responses.
func toPriceSetSummaryDTO(set models.PriceSet) priceSetDTO {
	return toPriceSetDTO(set, nil)
}

// toPriceDTOs converts the price slice into response bodies.
func toPriceDTOs(prices []models.Price) []priceDTO {
	out := make([]priceDTO, 0, len(prices))
	for i := range prices {
		out = append(out, toPriceDTO(prices[i]))
	}
	return out
}

// toPriceDTO converts the price model into the response body.
func toPriceDTO(price models.Price) priceDTO {
	return priceDTO{
		ID:           price.ID,
		PriceSetID:   price.PriceSetID,
		PriceListID:  price.PriceListID,
		CurrencyCode: price.CurrencyCode,
		Amount:       price.Amount,
		MinQuantity:  price.MinQuantity,
		MaxQuantity:  price.MaxQuantity,
		Rules:        toPriceRuleDTOs(price.Rules),
		CreatedAt:    price.CreatedAt,
		UpdatedAt:    price.UpdatedAt,
	}
}

// toPriceRuleDTOs converts the rule slice into response bodies.
func toPriceRuleDTOs(rules []models.PriceRule) []priceRuleDTO {
	out := make([]priceRuleDTO, 0, len(rules))
	for i := range rules {
		out = append(out, toPriceRuleDTO(rules[i]))
	}
	return out
}

// toPriceRuleDTO converts the rule model into the response body.
func toPriceRuleDTO(rule models.PriceRule) priceRuleDTO {
	values := rule.Values
	if values == nil {
		values = []string{}
	}
	return priceRuleDTO{
		ID:        rule.ID,
		PriceID:   rule.PriceID,
		Attribute: rule.Attribute,
		Operator:  string(rule.Operator),
		Values:    values,
		CreatedAt: rule.CreatedAt,
		UpdatedAt: rule.UpdatedAt,
	}
}

// toPriceListDTO converts the price list model into the response body.
func toPriceListDTO(list models.PriceList) priceListDTO {
	return priceListDTO{
		ID:          list.ID,
		Title:       list.Title,
		Description: list.Description,
		Type:        string(list.Type),
		Status:      string(list.Status),
		StartsAt:    list.StartsAt,
		EndsAt:      list.EndsAt,
		CreatedAt:   list.CreatedAt,
		UpdatedAt:   list.UpdatedAt,
	}
}

// toCalculatedPriceDTO converts the calculation result into the response body.
func toCalculatedPriceDTO(calculated models.CalculatedPrice) calculatedPriceDTO {
	return calculatedPriceDTO{
		PriceID:       calculated.PriceID,
		PriceSetID:    calculated.PriceSetID,
		CurrencyCode:  calculated.CurrencyCode,
		Amount:        calculated.Amount,
		Quantity:      calculated.Quantity,
		Total:         calculated.Total,
		MinQuantity:   calculated.MinQuantity,
		MaxQuantity:   calculated.MaxQuantity,
		PriceListID:   calculated.PriceListID,
		PriceListType: listTypeOrNil(calculated.PriceListType),
		MatchedRules:  calculated.MatchedRules,
	}
}

// toPriceInputs converts request bodies into service inputs.
//
// NO validation is done: the service decides on validity, and having a single
// validation site makes sure that HTTP and the cross-module call see the same
// rules.
func toPriceInputs(requests []priceRequest) []service.PriceInput {
	out := make([]service.PriceInput, 0, len(requests))
	for _, req := range requests {
		out = append(out, service.PriceInput{
			CurrencyCode: req.CurrencyCode,
			Amount:       req.Amount,
			MinQuantity:  req.MinQuantity,
			MaxQuantity:  req.MaxQuantity,
			PriceListID:  req.PriceListID,
			Rules:        toRuleInputs(req.Rules),
		})
	}
	return out
}

// toRuleInputs converts rule requests into service inputs.
func toRuleInputs(requests []ruleRequest) []service.RuleInput {
	out := make([]service.RuleInput, 0, len(requests))
	for _, req := range requests {
		out = append(out, service.RuleInput{
			Attribute: req.Attribute,
			Operator:  models.RuleOperator(req.Operator),
			Values:    req.Values,
		})
	}
	return out
}

// toPriceListInput converts the request body into the service input.
func toPriceListInput(req priceListRequest) service.PriceListInput {
	return service.PriceListInput{
		Title:       req.Title,
		Description: req.Description,
		Type:        models.PriceListType(req.Type),
		Status:      models.PriceListStatus(req.Status),
		StartsAt:    req.StartsAt,
		EndsAt:      req.EndsAt,
	}
}
