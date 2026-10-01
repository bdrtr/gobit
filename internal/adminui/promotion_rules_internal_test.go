package adminui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/query"
)

// fakeRuleEditor reads one page as scripted and records each rule write.
type fakeRuleEditor struct {
	fakePromotionReader
	added   []string
	removed []string
	err     error
}

func (f *fakeRuleEditor) AddPromotionRule(_ context.Context, promotionID, ruleType, attribute, operator string, values []string) error {
	f.added = append(f.added, fmt.Sprintf("%s|%s|%s|%s|%s", promotionID, ruleType, attribute, operator, strings.Join(values, ",")))
	return f.err
}

func (f *fakeRuleEditor) RemovePromotionRule(_ context.Context, promotionID, ruleID string) error {
	f.removed = append(f.removed, promotionID+"|"+ruleID)
	return f.err
}

// itemsPromotion is a percentage off the items with one category rule.
const itemsPromotion = `{"id":"promo_1","code":"SHOES","type":"standard","status":"active",
	"campaign":null,"latest_uses":[],
	"application_method":{"type":"percentage","target_type":"items","allocation":"each","value":2000},
	"rules":[{"id":"prule_1","type":"target","attribute":"category_tree_ids","operator":"any_in","values":["pcat_shoes","pcat_gone"]}]}`

// rulesPanel is a panel whose catalog knows two categories.
func rulesPanel(t *testing.T, editor PromotionLister) *UI {
	t.Helper()

	panel := newCatalogPanel(t, &fakeCatalog{byEntity: map[string][]query.Record{
		EntityCategory: {
			{fieldID: "pcat_shoes", fieldName: "Shoes"},
			{fieldID: "pcat_bags", fieldName: "Bags"},
		},
	}})
	panel.promotions = editor

	return panel
}

// rulesRequest sends one request through the page's routes.
func rulesRequest(panel *UI, method, path string, form url.Values, scopes ...string) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	r.Get(PromotionPath, panel.showPromotion)
	r.Post(PromotionRulesPath, panel.addCategoryRule)
	r.Post(PromotionRuleRemovePath, panel.removeRule)

	request := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request = request.WithContext(corehttp.WithPrincipal(request.Context(),
		corehttp.Principal{ID: "user_1", Kind: "user", Scopes: scopes}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, request)

	return rec
}

// TestAPromotionsRulesAreEditedOnItsPage is ADR 0315: a writer who reads the
// catalog sees a category rule's categories by name, a remove form on each
// rule and the category form; a reader of promotions alone sees the ids and
// neither form.
func TestAPromotionsRulesAreEditedOnItsPage(t *testing.T) {
	t.Parallel()

	editor := &fakeRuleEditor{fakePromotionReader: fakePromotionReader{page: itemsPromotion}}
	panel := rulesPanel(t, editor)

	rec := rulesRequest(panel, http.MethodGet, PromotionsPath+"/promo_1", nil,
		scopePromotionRead, scopePromotionWrite, scopeProductRead)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	assert.Contains(t, body, "<td>Shoes, pcat_gone</td>", "a known category by name, an unknown one by id")
	assert.Contains(t, body, `action="`+PromotionsPath+`/promo_1/rules/prule_1/remove"`)
	assert.Contains(t, body, `action="`+PromotionsPath+`/promo_1/rules"`)
	assert.Contains(t, body, `<option value="pcat_bags">Bags</option>`)

	rec = rulesRequest(panel, http.MethodGet, PromotionsPath+"/promo_1", nil, scopePromotionRead)
	body = rec.Body.String()
	assert.Contains(t, body, "<td>pcat_shoes, pcat_gone</td>", "no catalog privilege, no names")
	assert.NotContains(t, body, "/remove\"")
	assert.NotContains(t, body, "/rules\"")

	rec = rulesRequest(panel, http.MethodGet, PromotionsPath+"/promo_1", nil, scopePromotionRead, scopeProductRead)
	assert.Contains(t, rec.Body.String(), "<td>Shoes, pcat_gone</td>", "a reader of the catalog sees the names")
	assert.NotContains(t, rec.Body.String(), "/rules\"", "and no form without the promotion's write")

	rec = rulesRequest(panel, http.MethodGet, PromotionsPath+"/promo_1", nil, scopePromotionRead, scopePromotionWrite)
	assert.Contains(t, rec.Body.String(), "/remove\"", "removing needs no catalog privilege")
	assert.NotContains(t, rec.Body.String(), "/rules\"", "choosing a category does")

	order := rulesPanel(t, &fakeRuleEditor{fakePromotionReader: fakePromotionReader{
		page: strings.Replace(itemsPromotion, `"target_type":"items"`, `"target_type":"order"`, 1)}})
	rec = rulesRequest(order, http.MethodGet, PromotionsPath+"/promo_1", nil,
		scopePromotionRead, scopePromotionWrite, scopeProductRead)
	assert.NotContains(t, rec.Body.String(), "/rules\"", "an order discount chooses no lines, so no category form")
}

// TestTheCategoryFormWritesOneTargetRule: the chosen categories become one
// rule matching any of them, and the operator returns to the page; none
// chosen is refused before the surface is asked.
func TestTheCategoryFormWritesOneTargetRule(t *testing.T) {
	t.Parallel()

	editor := &fakeRuleEditor{fakePromotionReader: fakePromotionReader{page: itemsPromotion}}
	panel := rulesPanel(t, editor)

	rec := rulesRequest(panel, http.MethodPost, PromotionsPath+"/promo_1/rules",
		url.Values{formCategory: {"pcat_shoes", " pcat_bags ", ""}}, scopePromotionRead, scopePromotionWrite)
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, PromotionsPath+"/promo_1", rec.Header().Get("Location"))
	assert.Equal(t, []string{"promo_1|target|category_tree_ids|any_in|pcat_shoes,pcat_bags"}, editor.added)

	rec = rulesRequest(panel, http.MethodPost, PromotionsPath+"/promo_1/rules", url.Values{},
		scopePromotionRead, scopePromotionWrite)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "Choose at least one category.")
	assert.Contains(t, rec.Body.String(), "SHOES", "the refusal is drawn on the page")
	assert.Len(t, editor.added, 1, "the surface is not asked")
}

// TestARuleIsRemovedThroughItsPromotion: the remove form names the
// promotion and the rule; a refusal is drawn on the page, to a writer who
// cannot read it as the reason alone, and a failure is not a refusal.
func TestARuleIsRemovedThroughItsPromotion(t *testing.T) {
	t.Parallel()

	editor := &fakeRuleEditor{fakePromotionReader: fakePromotionReader{page: itemsPromotion}}
	panel := rulesPanel(t, editor)

	rec := rulesRequest(panel, http.MethodPost, PromotionsPath+"/promo_1/rules/prule_1/remove", nil,
		scopePromotionRead, scopePromotionWrite)
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, PromotionsPath+"/promo_1", rec.Header().Get("Location"))
	assert.Equal(t, []string{"promo_1|prule_1"}, editor.removed)

	editor.err = errors.NotFound("promotion_rule_not_on_promotion", "promotion promo_1 has no rule prule_9")
	rec = rulesRequest(panel, http.MethodPost, PromotionsPath+"/promo_1/rules/prule_9/remove", nil,
		scopePromotionRead, scopePromotionWrite)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "has no rule prule_9")
	assert.Contains(t, rec.Body.String(), "SHOES")

	rec = rulesRequest(panel, http.MethodPost, PromotionsPath+"/promo_1/rules/prule_9/remove", nil, scopePromotionWrite)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "has no rule prule_9")
	assert.NotContains(t, rec.Body.String(), "SHOES", "a writer who cannot read is shown none of the page")

	editor.err = errors.Unavailable("db_down", "no answer")
	rec = rulesRequest(panel, http.MethodPost, PromotionsPath+"/promo_1/rules/prule_1/remove", nil,
		scopePromotionRead, scopePromotionWrite)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)

	reading := rulesPanel(t, &fakePromotionReader{page: itemsPromotion})
	rec = rulesRequest(reading, http.MethodPost, PromotionsPath+"/promo_1/rules/prule_1/remove", nil, scopePromotionWrite)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code, "a surface that cannot write a rule says so")
	rec = rulesRequest(reading, http.MethodGet, PromotionsPath+"/promo_1", nil,
		scopePromotionRead, scopePromotionWrite, scopeProductRead)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), "/remove\"", "and its page offers no removal")
	assert.NotContains(t, rec.Body.String(), "/rules\"", "nor a category form")
}
