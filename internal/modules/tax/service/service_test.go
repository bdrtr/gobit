package service

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
)

// TestCreateTaxRegionCountryRoot checks the happy path of creating a root
// region.
func TestCreateTaxRegionCountryRoot(t *testing.T) {
	svc, _ := newTestService(t)

	region, err := svc.CreateTaxRegion(context.Background(), CreateTaxRegionInput{
		CountryCode: " tr ",
		Metadata:    map[string]any{"source": "test"},
	})
	require.NoError(t, err)

	assert.Equal(t, "TR", region.CountryCode, "the country code has to be trimmed and turned into UPPER case")
	assert.True(t, region.IsRoot())
	assert.Nil(t, region.ProvinceCode)
	assert.True(t, strings.HasPrefix(region.ID, models.TaxRegionIDPrefix))
	assert.Len(t, region.ID, len(models.TaxRegionIDPrefix)+models.IDBodyLength())
	assert.Equal(t, testNow, region.CreatedAt)
	assert.Equal(t, map[string]any{"source": "test"}, region.Metadata)
}

// TestCreateTaxRegionRejectsASecondRoot checks the one-root-per-country rule.
func TestCreateTaxRegionRejectsASecondRoot(t *testing.T) {
	svc, _ := newTestService(t)
	_, err := svc.CreateTaxRegion(context.Background(), CreateTaxRegionInput{CountryCode: "TR"})
	require.NoError(t, err)

	_, err = svc.CreateTaxRegion(context.Background(), CreateTaxRegionInput{CountryCode: "tr"})
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err))
	assert.Equal(t, CodeRootExists, errors.CodeOf(err))
}

// TestCreateTaxRegionValidatesTheProvider shows that the provider id is
// validated against the registry BEFORE the region is WRITTEN.
//
// The cost of an id that is not validated is delayed and large: a typo shows up
// not at write time but at the FIRST cart calculation in that country, as
// KindInternal (500), and until then no cart in the country closes.
func TestCreateTaxRegionValidatesTheProvider(t *testing.T) {
	t.Run("a provider that is not registered is rejected before anything is written", func(t *testing.T) {
		svc, repo := newTestService(t)

		_, err := svc.CreateTaxRegion(context.Background(), CreateTaxRegionInput{
			CountryCode: "DE", ProviderID: "  no such provider exists  ",
		})
		require.Error(t, err)
		assert.True(t, errors.IsInvalid(err), "an administrator's typo has to be a 422, not a 500")
		assert.Equal(t, CodeProviderNotFound, errors.CodeOf(err))
		assert.Contains(t, err.Error(), LocalProviderID, "the message has to name the registered ids")
		assert.Zero(t, repo.callCount("CreateTaxRegion"), "no row may be written")
	})

	t.Run("an unbounded provider id is rejected", func(t *testing.T) {
		svc, repo := newTestService(t)

		_, err := svc.CreateTaxRegion(context.Background(), CreateTaxRegionInput{
			CountryCode: "DE", ProviderID: strings.Repeat("a", maxIDLen+1),
		})
		require.Error(t, err)
		assert.True(t, errors.IsInvalid(err))
		assert.Zero(t, repo.callCount("CreateTaxRegion"))
	})

	t.Run("the id is stored trimmed", func(t *testing.T) {
		svc, _ := newTestService(t)

		region, err := svc.CreateTaxRegion(context.Background(), CreateTaxRegionInput{
			CountryCode: "DE", ProviderID: "  " + LocalProviderID + "  ",
		})
		require.NoError(t, err)
		assert.Equal(t, LocalProviderID, region.ProviderID,
			"the stored value must NOT DIVERGE from the value applied in the calculation")
	})

	t.Run("an empty id is allowed", func(t *testing.T) {
		svc, _ := newTestService(t)

		region, err := svc.CreateTaxRegion(context.Background(), CreateTaxRegionInput{CountryCode: "DE"})
		require.NoError(t, err)
		assert.Empty(t, region.ProviderID, "an empty id means inherit/local and is allowed")
	})
}

// TestCreateTaxRegionProvince checks that a province region is bound to the
// root.
func TestCreateTaxRegionProvince(t *testing.T) {
	svc, _ := newTestService(t)
	root, err := svc.CreateTaxRegion(context.Background(), CreateTaxRegionInput{CountryCode: "US"})
	require.NoError(t, err)

	province, err := svc.CreateTaxRegion(context.Background(), CreateTaxRegionInput{
		CountryCode:  "US",
		ProvinceCode: "ca",
		ParentID:     root.ID,
	})
	require.NoError(t, err)

	assert.False(t, province.IsRoot())
	assert.Equal(t, "CA", province.Province(), "the province code has to be turned into UPPER case")
	assert.Equal(t, root.ID, province.Parent())
}

// TestCreateTaxRegionRejectsAHalfHierarchy checks the requirement that the
// parent/province pair is given together.
func TestCreateTaxRegionRejectsAHalfHierarchy(t *testing.T) {
	svc, _ := newTestService(t)
	root, err := svc.CreateTaxRegion(context.Background(), CreateTaxRegionInput{CountryCode: "US"})
	require.NoError(t, err)

	t.Run("a province code but no parent", func(t *testing.T) {
		_, err := svc.CreateTaxRegion(context.Background(), CreateTaxRegionInput{
			CountryCode: "US", ProvinceCode: "CA",
		})
		require.Error(t, err)
		assert.True(t, errors.IsInvalid(err))
	})

	t.Run("a parent but no province code", func(t *testing.T) {
		_, err := svc.CreateTaxRegion(context.Background(), CreateTaxRegionInput{
			CountryCode: "US", ParentID: root.ID,
		})
		require.Error(t, err)
		assert.True(t, errors.IsInvalid(err))
	})
}

// TestCreateTaxRegionValidatesTheParent checks the root's existence, kind and
// COUNTRY checks.
func TestCreateTaxRegionValidatesTheParent(t *testing.T) {
	svc, _ := newTestService(t)
	usRoot, err := svc.CreateTaxRegion(context.Background(), CreateTaxRegionInput{CountryCode: "US"})
	require.NoError(t, err)
	province, err := svc.CreateTaxRegion(context.Background(), CreateTaxRegionInput{
		CountryCode: "US", ProvinceCode: "CA", ParentID: usRoot.ID,
	})
	require.NoError(t, err)

	t.Run("no parent", func(t *testing.T) {
		_, err := svc.CreateTaxRegion(context.Background(), CreateTaxRegionInput{
			CountryCode: "US", ProvinceCode: "NY",
			ParentID: models.TaxRegionIDPrefix + "MISSING00000000000000000000",
		})
		require.Error(t, err)
		assert.True(t, errors.IsNotFound(err))
	})

	t.Run("the parent cannot be a province", func(t *testing.T) {
		_, err := svc.CreateTaxRegion(context.Background(), CreateTaxRegionInput{
			CountryCode: "US", ProvinceCode: "NY", ParentID: province.ID,
		})
		require.Error(t, err)
		assert.True(t, errors.IsInvalid(err))
		assert.Equal(t, CodeParentInvalid, errors.CodeOf(err))
	})

	t.Run("the parent's country cannot differ", func(t *testing.T) {
		_, err := svc.CreateTaxRegion(context.Background(), CreateTaxRegionInput{
			CountryCode: "DE", ProvinceCode: "BY", ParentID: usRoot.ID,
		})
		require.Error(t, err)
		assert.Equal(t, CodeParentInvalid, errors.CodeOf(err))
	})

	t.Run("the parent id cannot be of the wrong kind", func(t *testing.T) {
		_, err := svc.CreateTaxRegion(context.Background(), CreateTaxRegionInput{
			CountryCode: "DE", ProvinceCode: "BY", ParentID: rateA,
		})
		require.Error(t, err)
		assert.True(t, errors.IsInvalid(err), "the prefix check has to give a validation error, not a 404")
	})
}

// TestDeleteTaxRegionCoversTheTree checks that the delete covers the child
// regions, the rates and the rules too.
func TestDeleteTaxRegionCoversTheTree(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(usRegionID, "US")
	repo.seedProvinceRegion(trIstanbul, "US", "CA", usRegionID)
	repo.seedDefaultRate(rateA, usRegionID, 2000)
	repo.seedRuledRate(rateB, trIstanbul, 100)
	repo.seedRule(ruleA, rateB, models.ReferenceProduct, "prod_1")

	require.NoError(t, svc.DeleteTaxRegion(context.Background(), usRegionID))

	_, err := svc.GetTaxRegion(context.Background(), usRegionID)
	assert.True(t, errors.IsNotFound(err))
	_, err = svc.GetTaxRegion(context.Background(), trIstanbul)
	assert.True(t, errors.IsNotFound(err), "the child region has to be deleted too")
	_, err = svc.GetTaxRate(context.Background(), rateA)
	assert.True(t, errors.IsNotFound(err), "the root region's rate has to be deleted too")
	_, err = svc.GetTaxRate(context.Background(), rateB)
	assert.True(t, errors.IsNotFound(err), "the child region's rate has to be deleted too")

	rules, err := repo.ListTaxRateRules(context.Background(), rateB)
	require.NoError(t, err)
	assert.Empty(t, rules, "the rate's rules have to be deleted too")

	// After the delete a new root has to be openable for the same country;
	// otherwise the delete would leave the country permanently impossible to
	// configure.
	_, err = svc.CreateTaxRegion(context.Background(), CreateTaxRegionInput{CountryCode: "US"})
	require.NoError(t, err)
}

// TestDeleteTaxRegionMissingRecord checks that NotFound comes back for a
// deleted/missing region.
func TestDeleteTaxRegionMissingRecord(t *testing.T) {
	svc, _ := newTestService(t)

	err := svc.DeleteTaxRegion(context.Background(), models.TaxRegionIDPrefix+"MISSING")
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err))
}

// TestListTaxRegionsFiltersAndPages checks the filter and paging contract.
func TestListTaxRegionsFiltersAndPages(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedRootRegion(usRegionID, "US")
	repo.seedProvinceRegion(trIstanbul, "TR", "34", trRegionID)

	all, err := svc.ListTaxRegions(context.Background(), "", 0, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(3), all.Count)
	assert.Equal(t, DefaultLimit, all.Limit, "if no limit is given the default has to apply")

	tr, err := svc.ListTaxRegions(context.Background(), "tr", 0, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(2), tr.Count, "the country filter has to work in lower case too")

	capped, err := svc.ListTaxRegions(context.Background(), "", MaxLimit+1000, 0)
	require.NoError(t, err)
	assert.Equal(t, MaxLimit, capped.Limit, "the clamped limit has to be reported in the result")

	_, err = svc.ListTaxRegions(context.Background(), "TUR", 0, 0)
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "a malformed filter must not be ignored silently")

	_, err = svc.ListTaxRegions(context.Background(), "", 0, -1)
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
}

// TestCreateTaxRateHappyPath checks creating a rate.
func TestCreateTaxRateHappyPath(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")

	rate, err := svc.CreateTaxRate(context.Background(), CreateTaxRateInput{
		TaxRegionID: trRegionID,
		Name:        "  KDV  ",
		Code:        " KDV20 ",
		RateBps:     2000,
		IsDefault:   true,
	})
	require.NoError(t, err)

	assert.Equal(t, "KDV", rate.Name, "the name has to be trimmed")
	assert.Equal(t, "KDV20", rate.RateCode())
	assert.True(t, strings.HasPrefix(rate.ID, models.TaxRateIDPrefix))

	percent, remainder := rate.RatePercent()
	assert.Equal(t, int32(20), percent)
	assert.Equal(t, int32(0), remainder)
}

// TestCreateTaxRateRejectsASecondDefault checks the one-default-per-region
// rule.
func TestCreateTaxRateRejectsASecondDefault(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 2000)

	_, err := svc.CreateTaxRate(context.Background(), CreateTaxRateInput{
		TaxRegionID: trRegionID, Name: "Second", RateBps: 1000, IsDefault: true,
	})
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err))
	assert.Equal(t, CodeDefaultExists, errors.CodeOf(err))

	// A second rate that is NOT the default is allowed.
	_, err = svc.CreateTaxRate(context.Background(), CreateTaxRateInput{
		TaxRegionID: trRegionID, Name: "Reduced", RateBps: 100,
	})
	require.NoError(t, err)
}

// TestCreateTaxRateInvalidInput checks the rate validations.
func TestCreateTaxRateInvalidInput(t *testing.T) {
	tests := map[string]CreateTaxRateInput{
		"empty region id":                  {Name: "KDV", RateBps: 100},
		"region id of the wrong kind":      {TaxRegionID: rateA, Name: "KDV", RateBps: 100},
		"empty name":                       {TaxRegionID: trRegionID, RateBps: 100},
		"name with a control character":    {TaxRegionID: trRegionID, Name: "KDV\nnew", RateBps: 100},
		"code with whitespace":             {TaxRegionID: trRegionID, Name: "KDV", Code: "KDV 20", RateBps: 100},
		"negative rate":                    {TaxRegionID: trRegionID, Name: "KDV", RateBps: -1},
		"rate exceeds one hundred percent": {TaxRegionID: trRegionID, Name: "KDV", RateBps: models.MaxRateBps + 1},
	}

	for name, in := range tests {
		t.Run(name, func(t *testing.T) {
			svc, repo := newTestService(t)
			repo.seedRootRegion(trRegionID, "TR")

			_, err := svc.CreateTaxRate(context.Background(), in)
			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err), "error: %v", err)
			assert.Zero(t, repo.callCount("CreateTaxRate"), "invalid input must not reach the repository")
		})
	}
}

// TestCreateTaxRateWithoutARegion checks that a rate without a region is
// rejected.
func TestCreateTaxRateWithoutARegion(t *testing.T) {
	svc, _ := newTestService(t)

	_, err := svc.CreateTaxRate(context.Background(), CreateTaxRateInput{
		TaxRegionID: trRegionID, Name: "KDV", RateBps: 2000,
	})
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err))
}

// TestUpdateTaxRateIsPartial checks that the patch touches only the given
// fields.
func TestUpdateTaxRateIsPartial(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	code := "KDV20"
	repo.seedRate(models.TaxRate{
		ID: rateA, TaxRegionID: trRegionID, Name: "KDV", Code: &code,
		RateBps: 2000, IsDefault: true, Metadata: map[string]any{"a": "b"},
	})

	newRate := int32(1800)
	updated, err := svc.UpdateTaxRate(context.Background(), rateA, UpdateTaxRateInput{RateBps: &newRate})
	require.NoError(t, err)

	assert.Equal(t, int32(1800), updated.RateBps)
	assert.Equal(t, "KDV", updated.Name, "a name not touched must not change")
	assert.Equal(t, "KDV20", updated.RateCode(), "a code not touched must not change")
	assert.True(t, updated.IsDefault, "a flag not touched must not change")
	assert.Equal(t, map[string]any{"a": "b"}, updated.Metadata)
}

// TestUpdateTaxRateRemovesTheCode checks that an empty string DELETES the code.
func TestUpdateTaxRateRemovesTheCode(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	code := "KDV20"
	repo.seedRate(models.TaxRate{ID: rateA, TaxRegionID: trRegionID, Name: "KDV", Code: &code, RateBps: 2000})

	empty := ""
	updated, err := svc.UpdateTaxRate(context.Background(), rateA, UpdateTaxRateInput{Code: &empty})
	require.NoError(t, err)
	assert.Nil(t, updated.Code)
	assert.Empty(t, updated.RateCode())
}

// TestUpdateTaxRateRejectsAnEmptyPatch checks that a silent success is not
// allowed.
func TestUpdateTaxRateRejectsAnEmptyPatch(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 2000)

	_, err := svc.UpdateTaxRate(context.Background(), rateA, UpdateTaxRateInput{})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
	assert.Zero(t, repo.callCount("UpdateTaxRate"))
}

// TestUpdateTaxRateARuledRateCannotBeMadeTheDefault checks that the scope
// conflict is prevented.
func TestUpdateTaxRateARuledRateCannotBeMadeTheDefault(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedRuledRate(rateB, trRegionID, 100)
	repo.seedRule(ruleA, rateB, models.ReferenceProduct, "prod_1")

	isDefault := true
	_, err := svc.UpdateTaxRate(context.Background(), rateB, UpdateTaxRateInput{IsDefault: &isDefault})
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err))
}

// TestUpdateTaxRateCannotMakeASecondDefault checks that the update path is
// subject to the uniqueness constraint too.
func TestUpdateTaxRateCannotMakeASecondDefault(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 2000)
	repo.seedRuledRate(rateB, trRegionID, 100)

	isDefault := true
	_, err := svc.UpdateTaxRate(context.Background(), rateB, UpdateTaxRateInput{IsDefault: &isDefault})
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err))
}

// TestDeleteTaxRateDeletesItsRulesToo checks that the rate's rules are deleted
// together with it.
func TestDeleteTaxRateDeletesItsRulesToo(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedRuledRate(rateB, trRegionID, 100)
	repo.seedRule(ruleA, rateB, models.ReferenceProduct, "prod_1")

	require.NoError(t, svc.DeleteTaxRate(context.Background(), rateB))

	rules, err := repo.ListTaxRateRules(context.Background(), rateB)
	require.NoError(t, err)
	assert.Empty(t, rules)

	err = svc.DeleteTaxRate(context.Background(), rateB)
	require.Error(t, err, "a second delete has to return NotFound")
	assert.True(t, errors.IsNotFound(err))
}

// TestCreateRateRuleHappyPath checks creating a rule.
func TestCreateRateRuleHappyPath(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedRuledRate(rateB, trRegionID, 100)

	rule, err := svc.CreateRateRule(context.Background(), CreateRateRuleInput{
		TaxRateID:   rateB,
		Reference:   "product_type",
		ReferenceID: "ptyp_food",
	})
	require.NoError(t, err)

	assert.Equal(t, models.ReferenceProductType, rule.Reference)
	assert.Equal(t, "ptyp_food", rule.ReferenceID)
	assert.True(t, strings.HasPrefix(rule.ID, models.TaxRateRuleIDPrefix))
}

// TestCreateRateRuleCannotBeAddedToTheDefaultRate checks the scope rule.
func TestCreateRateRuleCannotBeAddedToTheDefaultRate(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 2000)

	_, err := svc.CreateRateRule(context.Background(), CreateRateRuleInput{
		TaxRateID: rateA, Reference: "product", ReferenceID: "prod_1",
	})
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err))
}

// TestCreateRateRuleInvalidInput checks the rule validations.
func TestCreateRateRuleInvalidInput(t *testing.T) {
	tests := map[string]CreateRateRuleInput{
		"empty rate id":                {Reference: "product", ReferenceID: "prod_1"},
		"rate id of the wrong kind":    {TaxRateID: trRegionID, Reference: "product", ReferenceID: "prod_1"},
		"undefined reference":          {TaxRateID: rateB, Reference: "variant", ReferenceID: "var_1"},
		"empty reference":              {TaxRateID: rateB, ReferenceID: "prod_1"},
		"empty reference id":           {TaxRateID: rateB, Reference: "product"},
		"reference id with whitespace": {TaxRateID: rateB, Reference: "product", ReferenceID: " prod_1"},
	}

	for name, in := range tests {
		t.Run(name, func(t *testing.T) {
			svc, repo := newTestService(t)
			repo.seedRootRegion(trRegionID, "TR")
			repo.seedRuledRate(rateB, trRegionID, 100)

			_, err := svc.CreateRateRule(context.Background(), in)
			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err), "error: %v", err)
			assert.Zero(t, repo.callCount("CreateTaxRateRule"))
		})
	}
}

// TestDeleteRateRuleDoesNotMakeTheRateTheDefault checks that deleting the last
// rule does not SILENTLY widen the rate.
func TestDeleteRateRuleDoesNotMakeTheRateTheDefault(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedRuledRate(rateB, trRegionID, 100)
	repo.seedRule(ruleA, rateB, models.ReferenceProduct, "prod_1")

	require.NoError(t, svc.DeleteRateRule(context.Background(), ruleA))

	rate, err := svc.GetTaxRate(context.Background(), rateB)
	require.NoError(t, err)
	assert.False(t, rate.IsDefault, "a rate left without rules must NOT BE the default")

	result, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
		CountryCode: "TR",
		Items:       []TaxableItem{{ID: "li_1", ProductID: "prod_1", Amount: 10_000}},
	})
	require.NoError(t, err)
	assert.Equal(t, int64(0), result.TaxTotal, "a rate left without rules must apply to no line item")
}

// TestListRateRulesWithoutARate checks that a missing rate returns NotFound.
func TestListRateRulesWithoutARate(t *testing.T) {
	svc, _ := newTestService(t)

	_, err := svc.ListRateRules(context.Background(), rateB)
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err))
}

// TestAnUnconfiguredServiceDoesNotPanic checks that a service set up without a
// repository returns a typed error.
func TestAnUnconfiguredServiceDoesNotPanic(t *testing.T) {
	svc := New(nil, Options{})

	_, err := svc.CalculateTax(context.Background(), CalculateTaxInput{CountryCode: "TR"})
	require.Error(t, err)
	assert.Equal(t, CodeUnconfigured, errors.CodeOf(err))

	_, err = svc.GetTaxRegion(context.Background(), trRegionID)
	require.Error(t, err)

	_, _, err = svc.DefaultRateForCountry(context.Background(), "TR")
	require.Error(t, err)
}
