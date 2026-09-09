package service

// This file is English because ADR 0012 makes language a property of the FILE
// and every new file is English.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
)

// The class ids of these tests, written by hand for the reason the rate ids are:
// the tie-break rule can only be shown with ids whose order is known.
const (
	classBooks       = models.TaxClassIDPrefix + "A0000000000000000000000000"
	classElectronics = models.TaxClassIDPrefix + "B0000000000000000000000000"
)

// seedClass writes a class straight into the store.
func (m *memRepo) seedClass(id, name string) models.TaxClass {
	m.mu.Lock()
	defer m.mu.Unlock()

	class := models.TaxClass{ID: id, Name: name, CreatedAt: testNow, UpdatedAt: testNow}
	m.classes[id] = class

	return class
}

// seedMember puts a product in a class.
func (m *memRepo) seedMember(classID, productID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.members[productID] = classID
}

// TestARuleWrittenForAClassTaxesEveryProductInIt is the capability.
//
// Before it, a rule could name ONE product or a type the catalog does not have,
// so a shop taxing books at one rate had to write a rule per book and rewrite
// them as the catalog grew.
func TestARuleWrittenForAClassTaxesEveryProductInIt(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 2000)
	repo.seedRuledRate(rateB, trRegionID, 100)
	repo.seedRule(ruleA, rateB, models.ReferenceTaxClass, classBooks)

	repo.seedClass(classBooks, "Books")
	repo.seedMember(classBooks, "prod_book_1")
	repo.seedMember(classBooks, "prod_book_2")

	result, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
		CountryCode: "TR",
		Items: []TaxableItem{
			{ID: "li_1", ProductID: "prod_book_1", Amount: 10000},
			{ID: "li_2", ProductID: "prod_book_2", Amount: 10000},
			{ID: "li_3", ProductID: "prod_tv", Amount: 10000},
		},
	})
	require.NoError(t, err)

	assert.Equal(t, int64(100), taxOfItem(t, result, "li_1").TaxAmount,
		"a book is taxed at the class rate")
	assert.Equal(t, int64(100), taxOfItem(t, result, "li_2").TaxAmount,
		"and so is every other product in the same class, with no rule of its own")
	assert.Equal(t, int64(2000), taxOfItem(t, result, "li_3").TaxAmount,
		"a product in no class falls through to the region's default, exactly as before")
}

// TestWhatWasSaidAboutTHISProductBeatsWhatWasSaidAboutItsClass holds the
// specificity order.
//
// The narrower statement wins: a rule for one product is what the merchant said
// about that product, and the class is what they said about a set.
func TestWhatWasSaidAboutTHISProductBeatsWhatWasSaidAboutItsClass(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 2000)
	repo.seedRuledRate(rateB, trRegionID, 100)
	repo.seedRule(ruleA, rateB, models.ReferenceTaxClass, classBooks)
	repo.seedRuledRate(rateC, trRegionID, 800)
	repo.seedRule(ruleB, rateC, models.ReferenceProduct, "prod_book_1")

	repo.seedClass(classBooks, "Books")
	repo.seedMember(classBooks, "prod_book_1")
	repo.seedMember(classBooks, "prod_book_2")

	result, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
		CountryCode: "TR",
		Items: []TaxableItem{
			{ID: "li_1", ProductID: "prod_book_1", Amount: 10000},
			{ID: "li_2", ProductID: "prod_book_2", Amount: 10000},
		},
	})
	require.NoError(t, err)

	assert.Equal(t, int64(800), taxOfItem(t, result, "li_1").TaxAmount,
		"the product rule wins over the class rule for the product it names")
	assert.Equal(t, int64(100), taxOfItem(t, result, "li_2").TaxAmount,
		"and changes nothing for the rest of the class")
}

// TestAClassBeatsAProductType is the other side of the order.
//
// gobit has no product type today, so the type key is always empty on a real
// request; the case is still pinned, because the day the catalog grows types
// the two references meet on one item and the winner must not be an accident.
func TestAClassBeatsAProductType(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 2000)
	repo.seedRuledRate(rateB, trRegionID, 100)
	repo.seedRule(ruleA, rateB, models.ReferenceTaxClass, classBooks)
	repo.seedRuledRate(rateC, trRegionID, 800)
	repo.seedRule(ruleB, rateC, models.ReferenceProductType, "type_paper")

	repo.seedClass(classBooks, "Books")
	repo.seedMember(classBooks, "prod_book_1")

	result, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
		CountryCode: "TR",
		Items: []TaxableItem{
			{ID: "li_1", ProductID: "prod_book_1", ProductTypeID: "type_paper", Amount: 10000},
		},
	})
	require.NoError(t, err)

	assert.Equal(t, int64(100), taxOfItem(t, result, "li_1").TaxAmount,
		"the class is the tax module's own word about a set of products; the type is the "+
			"catalog's, and the module's own word is the more specific one")
}

// TestTheClassesOfAWholeCartAreReadInONEQuery keeps the lookup off the N+1 path.
func TestTheClassesOfAWholeCartAreReadInONEQuery(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 2000)
	repo.seedClass(classBooks, "Books")
	repo.seedMember(classBooks, "prod_1")

	_, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
		CountryCode: "TR",
		Items: []TaxableItem{
			{ID: "li_1", ProductID: "prod_1", Amount: 100},
			{ID: "li_2", ProductID: "prod_2", Amount: 100},
			{ID: "li_3", ProductID: "prod_3", Amount: 100},
			{ID: "li_4", ProductID: "prod_1", Amount: 100},
		},
	})
	require.NoError(t, err)

	assert.Equal(t, 1, repo.calls["ClassesOfProducts"],
		"four lines, one question; the repeated product is asked about once")
}

// TestACalculationWithNoProductAsksNothing keeps the query off a request that
// could not use the answer.
func TestACalculationWithNoProductAsksNothing(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 2000)

	_, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
		CountryCode: "TR",
		Items:       []TaxableItem{{ID: "li_1", Amount: 100}},
	})
	require.NoError(t, err)

	assert.Zero(t, repo.calls["ClassesOfProducts"],
		"a line with no product carries no class key, so there is nothing to ask")
}

// TestReclassifyingAProductMovesIt holds the write's shape.
//
// Refusing the second write would make an operator delete the first membership,
// leaving a window in which the product is in no class at all.
func TestReclassifyingAProductMovesIt(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedClass(classBooks, "Books")
	repo.seedClass(classElectronics, "Electronics")

	_, err := svc.SetProductTaxClass(context.Background(), classBooks, "prod_1")
	require.NoError(t, err)
	_, err = svc.SetProductTaxClass(context.Background(), classElectronics, "prod_1")
	require.NoError(t, err)

	classes, err := svc.repo.ClassesOfProducts(context.Background(), []string{"prod_1"})
	require.NoError(t, err)
	assert.Equal(t, classElectronics, classes["prod_1"],
		"the product is in the class it was last put in, and in only that one")

	books, err := svc.ListTaxClassMembers(context.Background(), classBooks)
	require.NoError(t, err)
	assert.Empty(t, books, "and it is out of the one it came from")
}

// TestAClassHoldingProductsCannotBeDeleted keeps a rule from applying to a set
// nothing can name.
func TestAClassHoldingProductsCannotBeDeleted(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedClass(classBooks, "Books")
	repo.seedMember(classBooks, "prod_1")

	err := svc.DeleteTaxClass(context.Background(), classBooks)

	require.Error(t, err)
	assert.Equal(t, coreerrors.KindConflict, coreerrors.KindOf(err))

	require.NoError(t, svc.RemoveProductTaxClass(context.Background(), "prod_1"))
	assert.NoError(t, svc.DeleteTaxClass(context.Background(), classBooks),
		"emptied, it retires")
}

// TestAClassNeedsAName refuses the write that names nothing.
func TestAClassNeedsAName(t *testing.T) {
	svc, _ := newTestService(t)

	_, err := svc.CreateTaxClass(context.Background(), CreateTaxClassInput{Name: "   "})

	require.Error(t, err)
	assert.Equal(t, coreerrors.KindInvalid, coreerrors.KindOf(err))
}
