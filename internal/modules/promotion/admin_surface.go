package promotion

import (
	"context"
	"encoding/json"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
	"github.com/bdrtr/gobit/internal/modules/promotion/service"
)

// AdminSurface is the promotion module's panel surface (ADR 0311): the
// promotions in a status with their usage, which the read provider keeps out
// because the read layer cannot tell a storefront from an operator. Only
// primitives and JSON cross it, as across every surface the panel resolves
// (ADR 0001).
type AdminSurface struct {
	svc *service.Service
}

// NewAdminSurface builds the panel's surface over the service.
func NewAdminSurface(svc *service.Service) *AdminSurface { return &AdminSurface{svc: svc} }

// codeAdminReadFailed reports a listing that could not be encoded.
const codeAdminReadFailed = "promotion_admin_read_failed"

// adminPromotion is one promotion as the panel lists it; the json tags are the
// contract with the panel, which cannot import this package.
type adminPromotion struct {
	ID          string    `json:"id"`
	Code        string    `json:"code"`
	IsAutomatic bool      `json:"is_automatic"`
	Type        string    `json:"type"`
	Status      string    `json:"status"`
	UsageCount  int64     `json:"usage_count"`
	UsageLimit  *int64    `json:"usage_limit"`
	CreatedAt   time.Time `json:"created_at"`
}

// PromotionsJSON lists the promotions in the status — draft, active or
// inactive — a page at a time, with the total in the status.
func (a *AdminSurface) PromotionsJSON(ctx context.Context, status string, limit, offset int32) (json.RawMessage, int64, error) {
	if a == nil || a.svc == nil {
		return nil, 0, errors.Unavailable(codeSetupFailed, "the promotion service is not set up")
	}

	wanted := models.PromotionStatus(status)
	page, err := a.svc.ListPromotions(ctx, service.ListPromotionsInput{Status: &wanted, Limit: limit, Offset: offset})
	if err != nil {
		return nil, 0, err
	}

	out := make([]adminPromotion, 0, len(page.Items))
	for i := range page.Items {
		p := &page.Items[i]
		out = append(out, adminPromotion{
			ID: p.ID, Code: p.Code, IsAutomatic: p.IsAutomatic, Type: string(p.Type),
			Status: string(p.Status), UsageCount: p.UsageCount, UsageLimit: p.UsageLimit,
			CreatedAt: p.CreatedAt,
		})
	}
	body, err := json.Marshal(out)
	if err != nil {
		return nil, 0, errors.Wrap(err, errors.KindInternal, codeAdminReadFailed,
			"the promotions could not be encoded")
	}

	return body, page.Count, nil
}

// SwitchPromotionStatus moves the promotion from the status the operator read
// to another, and refuses when it is no longer in the first (ADR 0312).
func (a *AdminSurface) SwitchPromotionStatus(ctx context.Context, id, from, to string) error {
	if a == nil || a.svc == nil {
		return errors.Unavailable(codeSetupFailed, "the promotion service is not set up")
	}

	_, err := a.svc.SwitchPromotionStatus(ctx, id, models.PromotionStatus(from), models.PromotionStatus(to))
	return err
}
