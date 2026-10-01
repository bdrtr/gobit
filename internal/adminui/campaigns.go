package adminui

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// The campaigns screen (ADR 0319): the promotion module's campaigns with their
// window and how much of their budget is used, and the form that writes one,
// through the module's panel surface.

// CampaignsPath lists the campaigns and takes the form that writes one.
const CampaignsPath = URLPrefix + "/campaigns"

// campaignsLabel is what the section is called on screen.
const campaignsLabel = "Campaigns"

// campaignsPerPage is the list's page size, the other lists'.
const campaignsPerPage = 25

// paramCreated carries the identifier of the campaign just written, in the
// address the form lands on.
const paramCreated = "created"

// The campaign form's fields (ADR 0319).
const (
	formCampaignName       = "name"
	formCampaignIdentifier = "campaign_identifier"
	formCampaignDesc       = "description"
	formCampaignStarts     = "starts_at"
	formCampaignEnds       = "ends_at"
	formBudgetType         = "budget_type"
	formBudgetLimit        = "budget_limit"
	formBudgetCurrency     = "budget_currency"
)

// The promotion module's budget types, as its surface spells them: a budget
// counted in uses, or in money spent in one currency.
const (
	budgetUsage = "usage"
	budgetSpend = "spend"
)

// CampaignLister is the narrow surface the screen reads through.
type CampaignLister interface {
	// CampaignsJSON lists the campaigns a page at a time, with the total.
	CampaignsJSON(ctx context.Context, limit, offset int32) (json.RawMessage, int64, error)
}

// CampaignCreator is the narrow surface a campaign is written through.
type CampaignCreator interface {
	// CreateCampaign writes a campaign and returns its id.
	CreateCampaign(
		ctx context.Context, name, identifier, description string, startsAt, endsAt *time.Time,
		budgetType string, budgetLimit *int64, currency string,
	) (string, error)
}

// campaignRow is one campaign as the module's surface sends it, on the list
// and on a promotion's page; the json tags are the contract with that
// surface, exercised end to end.
type campaignRow struct {
	ID                 string     `json:"id"`
	Name               string     `json:"name"`
	CampaignIdentifier string     `json:"campaign_identifier"`
	Description        string     `json:"description"`
	StartsAt           *time.Time `json:"starts_at"`
	EndsAt             *time.Time `json:"ends_at"`
	BudgetType         string     `json:"budget_type"`
	BudgetLimit        *int64     `json:"budget_limit"`
	BudgetUsed         int64      `json:"budget_used"`
	BudgetCurrencyCode string     `json:"budget_currency_code"`
}

// budgetText says how much of the campaign's budget is used, in uses or in
// its currency; empty when it has no budget.
func (c *campaignRow) budgetText(scales map[string]int) string {
	if c.BudgetLimit == nil {
		return ""
	}
	switch c.BudgetType {
	case budgetUsage:
		return strconv.FormatInt(c.BudgetUsed, 10) + " of " + strconv.FormatInt(*c.BudgetLimit, 10) + " uses"
	case budgetSpend:
		return minorText(c.BudgetUsed, c.BudgetCurrencyCode, scales) + " of " +
			minorText(*c.BudgetLimit, c.BudgetCurrencyCode, scales)
	}

	return ""
}

// minorText prints an amount in minor units with its currency, as
// [withCurrency] does.
func minorText(minor int64, currency string, scales map[string]int) string {
	text, known := formatAmount(minor, currency, scales)

	return withCurrency(text, currency, known)
}

// canCreateCampaigns reports whether the operator may write a campaign here.
func (u *UI) canCreateCampaigns(r *http.Request) bool {
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	_, ok := u.promotions.(CampaignCreator)

	return ok && principal.HasScope(scopePromotionWrite)
}

// listCampaigns renders the campaigns.
func (u *UI) listCampaigns(w http.ResponseWriter, r *http.Request) {
	u.renderCampaigns(w, r, http.StatusOK, "", url.Values{})
}

// createCampaign writes the campaign the form describes and returns to the
// list, which names it; a refusal comes back on the list with what was typed
// (ADR 0319).
func (u *UI) createCampaign(w http.ResponseWriter, r *http.Request) {
	creator, ok := u.promotions.(CampaignCreator)
	if !ok {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Campaigns unavailable",
			"The promotion module's panel surface cannot write a campaign in this installation.")
		return
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}

	err := u.writeCampaign(r, creator)
	switch {
	case err == nil:
		created := url.Values{paramCreated: {strings.TrimSpace(r.PostFormValue(formCampaignIdentifier))}}
		corehttp.WriteRedirect(r.Context(), w, CampaignsPath+"?"+created.Encode())
	case errors.IsInvalid(err) || errors.IsConflict(err):
		u.renderCampaigns(w, r, http.StatusUnprocessableEntity, messageFor(err), r.PostForm)
	default:
		u.unexpectedFailure(w, r, err, "The campaign could not be written")
	}
}

// writeCampaign reads the form into the surface's terms: the moments in UTC,
// as the product's schedule is read (ADR 0178), and a spend limit into its
// currency's minor units, or taken as minor units when the currency's scale is
// unknown, as a price is.
func (u *UI) writeCampaign(r *http.Request, creator CampaignCreator) error {
	startsAt, err := readCampaignMoment(r.PostFormValue(formCampaignStarts), "start")
	if err != nil {
		return err
	}
	endsAt, err := readCampaignMoment(r.PostFormValue(formCampaignEnds), "end")
	if err != nil {
		return err
	}

	budgetType := r.PostFormValue(formBudgetType)
	currency := strings.ToUpper(strings.TrimSpace(r.PostFormValue(formBudgetCurrency)))
	var limit *int64
	if text := strings.TrimSpace(r.PostFormValue(formBudgetLimit)); text != "" {
		var n int64
		if budgetType == budgetSpend {
			scale, known := u.currencyScales(r.Context())[currency]
			n, err = parseAmount(text, scale, !known)
		} else if n, err = strconv.ParseInt(text, 10, 64); err != nil {
			err = errors.Invalid(CodeAmountInvalid, "A budget in uses is a whole number.")
		}
		if err != nil {
			return err
		}
		limit = &n
	}

	_, err = creator.CreateCampaign(r.Context(),
		strings.TrimSpace(r.PostFormValue(formCampaignName)),
		strings.TrimSpace(r.PostFormValue(formCampaignIdentifier)),
		strings.TrimSpace(r.PostFormValue(formCampaignDesc)),
		startsAt, endsAt, budgetType, limit, currency)

	return err
}

// readCampaignMoment reads one end of the window in UTC; nil for an empty
// field, which leaves that end open. A past start is a campaign already
// running, so no moment is refused for being past.
func readCampaignMoment(value, end string) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	at, err := time.ParseInLocation(publishAtLayout, value, time.UTC)
	if err != nil {
		return nil, errors.Invalid(CodeMomentInvalid,
			"The campaign's %s could not be read; use the date and time picker.", end)
	}

	return &at, nil
}

// campaignView is one campaign as the list prints it.
type campaignView struct {
	campaignRow
	Budget string
}

// renderCampaigns lists the campaigns with a refused write's reason and what
// was typed. An operator who may write and not read the list is told the
// reason alone (ADR 0260).
func (u *UI) renderCampaigns(w http.ResponseWriter, r *http.Request, code int, refused string, typed url.Values) {
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	if refused != "" && !principal.HasScope(scopePromotionRead) {
		u.errorPage(w, r, code, "Not done", refused)
		return
	}
	lister, ok := u.promotions.(CampaignLister)
	if !ok {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Campaigns unavailable",
			"The promotion module's panel surface cannot list the campaigns in this installation.")
		return
	}

	page := pageNumber(r.URL.Query().Get("page"))
	offset := (page - 1) * campaignsPerPage
	if offset > math.MaxInt32 {
		offset = math.MaxInt32
	}
	raw, total, err := lister.CampaignsJSON(r.Context(), campaignsPerPage, int32(offset))
	if err != nil {
		u.unexpectedFailure(w, r, err, "The campaigns could not be read")
		return
	}
	var rows []campaignRow
	if err := json.Unmarshal(raw, &rows); err != nil {
		u.unexpectedFailure(w, r, err, "The campaigns could not be read")
		return
	}

	scales := u.currencyScales(r.Context())
	views := make([]campaignView, 0, len(rows))
	for i := range rows {
		views = append(views, campaignView{campaignRow: rows[i], Budget: rows[i].budgetText(scales)})
	}
	data := map[string]any{
		titleKey:    campaignsLabel,
		"Campaigns": views,
		totalKey:    total,
		"Created":   r.URL.Query().Get(paramCreated),
		"CanCreate": u.canCreateCampaigns(r),
		refusedKey:  refused,
		typedKey:    typed,
	}
	addPaging(data, page, int64(page*campaignsPerPage) < total, CampaignsPath)

	u.templates.render(w, r, code, "campaigns.gohtml", data)
}
