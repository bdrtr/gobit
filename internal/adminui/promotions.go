package adminui

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"slices"
	"time"
)

// The promotions screen (ADR 0311): the shop's promotions in one status at a
// time with their usage, read through the promotion module's panel surface
// because its read provider keeps drafts, inactive promotions and usage out.

// ServicePromotionAdmin is the promotion module's panel surface, spelled by
// hand and pinned against the module's constant in internal/arch.
const ServicePromotionAdmin = "promotion.admin"

// PromotionsPath lists the promotions.
const PromotionsPath = URLPrefix + "/promotions"

// promotionsLabel is what the section is called on screen.
const promotionsLabel = "Promotions"

// scopePromotionRead is the promotion module's read privilege, as the admin
// API names it.
const scopePromotionRead = "promotion:read"

// paramPromotionStatus chooses the status listed; the promotion module's
// statuses, live ones first.
const paramPromotionStatus = "status"

// promotionStatuses are the statuses the screen offers, in the order of its
// tabs; the first is listed when none is chosen.
var promotionStatuses = []string{"active", "draft", "inactive"}

// promotionsPerPage is the list's page size, the other lists'.
const promotionsPerPage = 25

// PromotionLister is the narrow surface the screen reads through (ADR 0001).
type PromotionLister interface {
	// PromotionsJSON lists the promotions in the status a page at a time, and
	// the total in the status.
	PromotionsJSON(ctx context.Context, status string, limit, offset int32) (json.RawMessage, int64, error)
}

// promotionRow is one promotion as the module's surface sends it; the json
// tags are the contract with that surface, exercised end to end.
type promotionRow struct {
	ID          string    `json:"id"`
	Code        string    `json:"code"`
	IsAutomatic bool      `json:"is_automatic"`
	Type        string    `json:"type"`
	Status      string    `json:"status"`
	UsageCount  int64     `json:"usage_count"`
	UsageLimit  *int64    `json:"usage_limit"`
	CreatedAt   time.Time `json:"created_at"`
}

// listPromotions renders the promotions in the chosen status.
func (u *UI) listPromotions(w http.ResponseWriter, r *http.Request) {
	if u.promotions == nil {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Promotions unavailable",
			"The promotion module's panel surface is not registered in this installation.")
		return
	}
	status := r.URL.Query().Get(paramPromotionStatus)
	if !slices.Contains(promotionStatuses, status) {
		status = promotionStatuses[0]
	}
	page := pageNumber(r.URL.Query().Get("page"))

	offset := (page - 1) * promotionsPerPage
	if offset > math.MaxInt32 {
		offset = math.MaxInt32
	}
	raw, total, err := u.promotions.PromotionsJSON(r.Context(), status,
		promotionsPerPage, int32(offset))
	if err != nil {
		u.unexpectedFailure(w, r, err, "The promotions could not be read")
		return
	}
	var rows []promotionRow
	if err := json.Unmarshal(raw, &rows); err != nil {
		u.unexpectedFailure(w, r, err, "The promotions could not be read")
		return
	}

	data := map[string]any{
		titleKey:     promotionsLabel,
		"Promotions": rows,
		"Status":     status,
		"Statuses":   promotionStatuses,
		"Total":      total,
	}
	addPaging(data, page, int64(page*promotionsPerPage) < total, PromotionsPath)

	u.templates.render(w, r, http.StatusOK, "promotions.gohtml", data)
}
