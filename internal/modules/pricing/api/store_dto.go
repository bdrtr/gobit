package api

import (
	"time"

	"github.com/bdrtr/gobit/internal/modules/pricing/models"
)

// storePriceSetDTO is the storefront's body of a price set (GET
// /store/v1/price-sets/{id}).
//
// It shared the admin body until ADR 0167, on the argument that the two had the
// same fields and differed only in which prices they held. They no longer have
// the same fields: a storefront price says what kind of list it comes from, and
// the sale price a shopper is charged now carries its reduction — which the
// admin body, built to show every price with its rules, has no reason to carry.
type storePriceSetDTO struct {
	// ID is the container's id.
	ID string `json:"id"`
	// Prices are the prices the storefront may show.
	Prices []storePriceDTO `json:"prices"`
	// CreatedAt is the moment of creation (RFC3339, UTC).
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is the moment of the last update (RFC3339, UTC).
	UpdatedAt time.Time `json:"updated_at"`
}

// storePriceDTO is one price the storefront may show.
type storePriceDTO struct {
	// ID is the price's id.
	ID string `json:"id"`
	// PriceSetID is the container the price belongs to.
	PriceSetID string `json:"price_set_id"`
	// PriceListID is the list the price is bound to; null for a base price.
	PriceListID *string `json:"price_list_id"`
	// PriceListType is that list's type — "sale" or "override" — and absent for
	// a base price. Without it a sale price and the price it reduces were two
	// unlabeled amounts.
	PriceListType *string `json:"price_list_type,omitempty"`
	// CurrencyCode is the ISO 4217 code (UPPERCASE).
	CurrencyCode string `json:"currency_code"`
	// Amount is the amount in minor units.
	Amount int64 `json:"amount"`
	// MinQuantity is the lower quantity bound.
	MinQuantity int32 `json:"min_quantity"`
	// MaxQuantity is the upper quantity bound; null when unbounded.
	MaxQuantity *int32 `json:"max_quantity"`
	// ReducedSince is when the reduction this sale price belongs to began; set
	// only on the sale price a shopper is charged now, and absent when the
	// reduction began before the price history did.
	ReducedSince *time.Time `json:"reduced_since,omitempty"`
	// LowestPriorAmount is the lowest price that applied in the thirty days
	// before ReducedSince — the price a shop announcing the reduction shows —
	// and absent whenever the history cannot state it.
	LowestPriorAmount *int64 `json:"lowest_prior_amount,omitempty"`
	// CreatedAt is the moment of creation.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is the moment of the last update.
	UpdatedAt time.Time `json:"updated_at"`
}

// toStorePriceSetDTO builds the storefront body of a price set.
func toStorePriceSetDTO(set models.PriceSet, prices []models.StorePrice) storePriceSetDTO {
	out := storePriceSetDTO{
		ID:        set.ID,
		Prices:    make([]storePriceDTO, 0, len(prices)),
		CreatedAt: set.CreatedAt,
		UpdatedAt: set.UpdatedAt,
	}
	for i := range prices {
		price := prices[i].Price
		dto := storePriceDTO{
			ID:           price.ID,
			PriceSetID:   price.PriceSetID,
			PriceListID:  price.PriceListID,
			CurrencyCode: price.CurrencyCode,
			Amount:       price.Amount,
			MinQuantity:  price.MinQuantity,
			MaxQuantity:  price.MaxQuantity,
			CreatedAt:    price.CreatedAt,
			UpdatedAt:    price.UpdatedAt,
		}
		if prices[i].ListType != "" {
			listType := string(prices[i].ListType)
			dto.PriceListType = &listType
		}
		if reduction := prices[i].Reduction; reduction != nil {
			dto.ReducedSince = reduction.ReducedSince
			dto.LowestPriorAmount = reduction.LowestPrior
		}
		out.Prices = append(out.Prices, dto)
	}

	return out
}
