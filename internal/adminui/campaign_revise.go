package adminui

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// A campaign's row revises the campaign's name, description, window and
// budget limit from what the row was drawn with (ADR 0331).

// CampaignRevisePath takes a row's form that revises its campaign.
const CampaignRevisePath = CampaignsPath + "/{id}"

// formReadBudgetLimit is the budget limit a row's form was drawn with, in its
// unit, beside the name, the description and the moments (ADR 0331).
const formReadBudgetLimit = "read_budget_limit"

// CampaignReviser is the narrow surface a campaign's terms are revised
// through (ADR 0331).
type CampaignReviser interface {
	// ReviseCampaign writes the campaign's name, description, window and
	// budget limit, and refuses when they are no longer the ones read; a nil
	// moment is an open end and a nil limit no limit.
	ReviseCampaign(
		ctx context.Context, id, readName, readDescription string, readStartsAt, readEndsAt *time.Time,
		readBudgetLimit *int64, name, description string, startsAt, endsAt *time.Time, budgetLimit *int64,
	) error
}

// canReviseCampaigns reports whether the operator may revise a campaign here.
func (u *UI) canReviseCampaigns(r *http.Request) bool {
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	_, ok := u.promotions.(CampaignReviser)

	return ok && principal.HasScope(scopePromotionWrite)
}

// reviseCampaign writes the terms a row's form was sent with, from the ones
// the row was drawn with, and returns to the list's page, which names the
// campaign; a refusal, a campaign another operator revised first included,
// comes back on the page with what was typed in the row (ADR 0331).
func (u *UI) reviseCampaign(w http.ResponseWriter, r *http.Request) {
	reviser, ok := u.promotions.(CampaignReviser)
	if !ok {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Campaigns unavailable",
			"The promotion module's panel surface cannot revise a campaign in this installation.")
		return
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}

	id := chi.URLParam(r, "id")
	name := strings.TrimSpace(r.PostFormValue(formCampaignName))
	err := u.sendCampaignRevision(r, reviser, id, name)
	switch {
	case err == nil:
		landing := url.Values{paramCreated: {name}}
		if page := pageNumber(r.URL.Query().Get("page")); page > 1 {
			landing.Set("page", strconv.Itoa(page))
		}
		corehttp.WriteRedirect(r.Context(), w, CampaignsPath+"?"+landing.Encode())
	case errors.IsInvalid(err) || errors.IsConflict(err) || errors.IsNotFound(err):
		u.renderCampaigns(w, r, http.StatusUnprocessableEntity, messageFor(err), r.PostForm, id)
	default:
		u.unexpectedFailure(w, r, err, "The campaign could not be revised")
	}
}

// sendCampaignRevision reads the row's drawn terms and the typed ones and
// sends them.
func (u *UI) sendCampaignRevision(r *http.Request, reviser CampaignReviser, id, name string) error {
	readStarts, err := drawnMoment(r.PostFormValue(formReadStarts))
	if err != nil {
		return err
	}
	readEnds, err := drawnMoment(r.PostFormValue(formReadEnds))
	if err != nil {
		return err
	}
	readLimit, err := drawnLimit(r.PostFormValue(formReadBudgetLimit))
	if err != nil {
		return err
	}
	startsAt, err := revisedMoment(r.PostFormValue(formCampaignStarts), readStarts, "campaign", "start")
	if err != nil {
		return err
	}
	endsAt, err := revisedMoment(r.PostFormValue(formCampaignEnds), readEnds, "campaign", "end")
	if err != nil {
		return err
	}
	limit, err := u.revisedLimit(r)
	if err != nil {
		return err
	}

	return reviser.ReviseCampaign(r.Context(), id,
		lineFeeds(r.PostFormValue(formReadName)), lineFeeds(r.PostFormValue(formReadDescription)),
		readStarts, readEnds, readLimit,
		name, strings.TrimSpace(lineFeeds(r.PostFormValue(formCampaignDesc))), startsAt, endsAt, limit)
}

// drawnLimit reads the budget limit a row was drawn with, in its unit, empty
// for none.
func drawnLimit(value string) (*int64, error) {
	if value == "" {
		return nil, nil
	}
	limit, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return nil, errors.Invalid("admin_ui_read_budget",
			"The budget the row was drawn with could not be read; draw the list again.")
	}

	return &limit, nil
}

// revisedLimit reads the typed budget limit in the unit of the budget the
// row was drawn with: uses, or the currency's decimals, or minor units when
// the panel cannot read the currency's scale, which is how the row shows it,
// so a limit left as shown is the limit drawn; an empty field is no limit.
func (u *UI) revisedLimit(r *http.Request) (*int64, error) {
	text := strings.TrimSpace(r.PostFormValue(formBudgetLimit))
	if text == "" {
		return nil, nil
	}

	var limit int64
	var err error
	if r.PostFormValue(formBudgetType) == budgetSpend {
		scale, known := u.currencyScales(r.Context())[r.PostFormValue(formBudgetCurrency)]
		limit, err = parseAmount(text, scale, !known)
	} else if limit, err = strconv.ParseInt(text, 10, 64); err != nil {
		err = errors.Invalid(CodeAmountInvalid, "A budget in uses is a whole number.")
	}
	if err != nil {
		return nil, err
	}

	return &limit, nil
}

// limitText prints a budget limit as the form shows it: uses, or the
// currency's decimals without the code.
func limitText(limit int64, budgetType, currency string, scales map[string]int) string {
	if budgetType != budgetSpend {
		return strconv.FormatInt(limit, 10)
	}
	text, _ := formatAmount(limit, currency, scales)

	return text
}

// campaignForm is what a campaign's row form offers: the campaign as drawn,
// or what was typed in the form a refusal came back to.
type campaignForm struct {
	ReadStarts      string
	ReadEnds        string
	ReadLimit       string
	FormName        string
	FormDescription string
	FormStarts      string
	FormEnds        string
	FormLimit       string
	Refused         bool
}

// campaignFormOf draws the row's form; the row revised, when one was,
// carries what was typed.
func campaignFormOf(row *campaignRow, scales map[string]int, typed url.Values, revised string) campaignForm {
	form := campaignForm{
		ReadStarts: drawnText(row.StartsAt, time.RFC3339Nano), ReadEnds: drawnText(row.EndsAt, time.RFC3339Nano),
		FormName: row.Name, FormDescription: row.Description,
		FormStarts: drawnText(row.StartsAt, publishAtLayout), FormEnds: drawnText(row.EndsAt, publishAtLayout),
	}
	if row.BudgetLimit != nil {
		form.ReadLimit = strconv.FormatInt(*row.BudgetLimit, 10)
		form.FormLimit = limitText(*row.BudgetLimit, row.BudgetType, row.BudgetCurrencyCode, scales)
	}
	if revised != "" && row.ID == revised {
		form.FormName, form.FormDescription = typed.Get(formCampaignName), typed.Get(formCampaignDesc)
		form.FormStarts, form.FormEnds = typed.Get(formCampaignStarts), typed.Get(formCampaignEnds)
		form.FormLimit, form.Refused = typed.Get(formBudgetLimit), true
	}

	return form
}
