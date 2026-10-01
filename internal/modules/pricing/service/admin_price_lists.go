package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/pricing/models"
)

// codeAdminEncodeFailed reports a listing the panel's surface could not
// encode.
const codeAdminEncodeFailed = "pricing_admin_encode_failed"

// adminPriceList is one price list as the panel lists it (ADR 0326); the json
// tags are the contract with the panel, which cannot import this package.
type adminPriceList struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Type        string     `json:"type"`
	Status      string     `json:"status"`
	StartsAt    *time.Time `json:"starts_at"`
	EndsAt      *time.Time `json:"ends_at"`
	CreatedAt   time.Time  `json:"created_at"`
}

// PriceListsJSON lists the price lists a page at a time in the module's
// order, with the total (ADR 0326).
func (a *AdminSurface) PriceListsJSON(ctx context.Context, limit, offset int32) (json.RawMessage, int64, error) {
	page, err := a.svc.ListPriceLists(ctx, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	out := make([]adminPriceList, 0, len(page.Items))
	for i := range page.Items {
		l := &page.Items[i]
		out = append(out, adminPriceList{
			ID: l.ID, Title: l.Title, Description: l.Description, Type: string(l.Type),
			Status: string(l.Status), StartsAt: l.StartsAt, EndsAt: l.EndsAt, CreatedAt: l.CreatedAt,
		})
	}
	body, err := json.Marshal(out)
	if err != nil {
		return nil, 0, errors.Wrap(err, errors.KindInternal, codeAdminEncodeFailed,
			"the price lists could not be encoded")
	}

	return body, page.Count, nil
}

// CreatePriceList writes a price list and returns its id (ADR 0326):
// listType is "sale", whose price is chosen over the base price, or
// "override", whose price is chosen over both; status is "draft", whose
// prices apply nowhere yet, or "active"; a nil moment leaves that end of the
// window open.
func (a *AdminSurface) CreatePriceList(
	ctx context.Context, title, description, listType, status string, startsAt, endsAt *time.Time,
) (string, error) {
	list, err := a.svc.CreatePriceList(ctx, PriceListInput{
		Title: title, Description: description, Type: models.PriceListType(listType),
		Status: models.PriceListStatus(status), StartsAt: startsAt, EndsAt: endsAt,
	})
	if err != nil {
		return "", err
	}

	return list.ID, nil
}
