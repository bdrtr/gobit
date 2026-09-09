//go:build integration

// This file is English because ADR 0012 makes language a property of the FILE
// and every new file is English; tax_integration_test.go beside it stays
// Turkish and lends its harness.
//
// The tests here run against a real PostgreSQL; to run them:
// make test-integration
//
// What they cover is the SCHEMA's half of the tax class: the partial unique
// index that keeps a product in at most one class, the widened rule vocabulary,
// and the class a membership points at. The unit tests prove the service's
// decisions against a fake, and a fake cannot disagree with a constraint it
// does not have.
package tax_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
	"github.com/bdrtr/gobit/internal/modules/tax/service"
)

// newTaxClass opens a class with a name nothing else in this run uses.
func newTaxClass(ctx context.Context, t *testing.T, svc *service.Service) models.TaxClass {
	t.Helper()

	class, err := svc.CreateTaxClass(ctx, service.CreateTaxClassInput{
		Name: "Class " + t.Name() + benzersizUlke(t),
	})
	require.NoError(t, err, "the fixture tax class could not be created")

	return class
}

// TestAProductIsInAtMostOneClassOnTheRealSchema is the invariant the whole
// selection rests on.
//
// With two classes a line would carry two match keys of equal specificity and
// which rate applied would fall to row order. The service moves the product;
// this is the proof that the DATABASE is what makes the move the only outcome.
func TestAProductIsInAtMostOneClassOnTheRealSchema(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)

	books := newTaxClass(ctx, t, svc)
	electronics := newTaxClass(ctx, t, svc)
	productID := "prod_" + books.ID

	_, err := svc.SetProductTaxClass(ctx, books.ID, productID)
	require.NoError(t, err)
	_, err = svc.SetProductTaxClass(ctx, electronics.ID, productID)
	require.NoError(t, err, "putting a product in a second class MOVES it")

	var rows int64
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM tax_class_member WHERE product_id = $1 AND deleted_at IS NULL`,
		productID).Scan(&rows))
	assert.Equal(t, int64(1), rows, "one live membership, never two")

	var classID string
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT tax_class_id FROM tax_class_member WHERE product_id = $1 AND deleted_at IS NULL`,
		productID).Scan(&classID))
	assert.Equal(t, electronics.ID, classID, "and it is the class it was last put in")
}

// TestASecondLiveClassCannotTakeTheSameName holds the name an operator picks by.
func TestASecondLiveClassCannotTakeTheSameName(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)

	first := newTaxClass(ctx, t, svc)

	_, err := svc.CreateTaxClass(ctx, service.CreateTaxClassInput{Name: first.Name})

	require.Error(t, err, "two live classes may not share the name the operator picks by")
	assert.Equal(t, coreerrors.KindConflict, coreerrors.KindOf(err))

	require.NoError(t, svc.DeleteTaxClass(ctx, first.ID))
	_, err = svc.CreateTaxClass(ctx, service.CreateTaxClassInput{Name: first.Name})
	assert.NoError(t, err, "a retired class keeps its name out of the way")
}

// TestAClassRuleIsAcceptedByTheWidenedVocabulary is the CHECK the migration
// widened.
//
// A word the schema refuses is a rule nobody can write, however well the Go
// side accepts it — the two vocabularies have to say the same thing.
func TestAClassRuleIsAcceptedByTheWidenedVocabulary(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)

	country := benzersizUlke(t)
	region, err := svc.CreateTaxRegion(ctx, service.CreateTaxRegionInput{CountryCode: country})
	require.NoError(t, err)

	rate, err := svc.CreateTaxRate(ctx, service.CreateTaxRateInput{
		TaxRegionID: region.ID, Name: "class rate", RateBps: 100,
	})
	require.NoError(t, err)

	class := newTaxClass(ctx, t, svc)

	rule, err := svc.CreateRateRule(ctx, service.CreateRateRuleInput{
		TaxRateID:   rate.ID,
		Reference:   models.ReferenceTaxClass.String(),
		ReferenceID: class.ID,
	})
	require.NoError(t, err, "a rule written for a class has to reach the table")
	assert.Equal(t, models.ReferenceTaxClass, rule.Reference)

	_, err = testPool.Pool().Exec(ctx,
		`UPDATE tax_rate_rule SET reference = 'variant' WHERE id = $1`, rule.ID)
	require.Error(t, err, "a word outside the vocabulary must not be writable")
	assert.Contains(t, err.Error(), "tax_rate_rule_reference_check")
}

// TestAMembershipCannotNameAClassThatIsNotThere holds the one foreign key this
// pair is allowed to have.
//
// The class is THIS module's record, so the reference is a real FK — unlike the
// product id beside it, which belongs to the catalog and is free TEXT
// (Principle 2.2).
func TestAMembershipCannotNameAClassThatIsNotThere(t *testing.T) {
	ctx := context.Background()

	_, err := testPool.Pool().Exec(ctx,
		`INSERT INTO tax_class_member (id, tax_class_id, product_id)
         VALUES ($1, $2, $3)`,
		models.NewTaxClassMemberID(time.Now()), "taxcls_MISSING", "prod_1")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "tax_class_member_class_fk")
}

// TestAStackLivesInsideOneRegionOnTheRealSchema holds the composite foreign
// key.
//
// A rate standing on one in ANOTHER region would be priced by a chain that
// never loads it, so the reference carries the region and the key is composite
// — the same trick tax_region_parent_fk uses one table up.
func TestAStackLivesInsideOneRegionOnTheRealSchema(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)

	first, err := svc.CreateTaxRegion(ctx, service.CreateTaxRegionInput{
		CountryCode: benzersizUlke(t),
	})
	require.NoError(t, err)
	second, err := svc.CreateTaxRegion(ctx, service.CreateTaxRegionInput{
		CountryCode: benzersizUlke(t),
	})
	require.NoError(t, err)

	base, err := svc.CreateTaxRate(ctx, service.CreateTaxRateInput{
		TaxRegionID: first.ID, Name: "base", RateBps: 500,
	})
	require.NoError(t, err)

	_, err = testPool.Pool().Exec(ctx,
		`INSERT INTO tax_rate (id, tax_region_id, name, rate_bps, stacks_on_id)
         VALUES ($1, $2, 'crossing', 100, $3)`,
		models.NewTaxRateID(time.Now()), second.ID, base.ID)

	require.Error(t, err, "a stack may not cross a region boundary")
	assert.Contains(t, err.Error(), "tax_rate_stacks_on_fk")
}

// TestOnlyOneRateStandsOnAnyRate keeps the stack a LIST.
//
// With two rates on one base the order of the two would be undecided and the
// same line could be taxed two ways.
func TestOnlyOneRateStandsOnAnyRate(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)

	region, err := svc.CreateTaxRegion(ctx, service.CreateTaxRegionInput{
		CountryCode: benzersizUlke(t),
	})
	require.NoError(t, err)

	base, err := svc.CreateTaxRate(ctx, service.CreateTaxRateInput{
		TaxRegionID: region.ID, Name: "base", RateBps: 500,
	})
	require.NoError(t, err)
	_, err = svc.CreateTaxRate(ctx, service.CreateTaxRateInput{
		TaxRegionID: region.ID, Name: "above", RateBps: 100, StacksOnID: base.ID,
	})
	require.NoError(t, err)

	_, err = testPool.Pool().Exec(ctx,
		`INSERT INTO tax_rate (id, tax_region_id, name, rate_bps, stacks_on_id)
         VALUES ($1, $2, 'second above', 100, $3)`,
		models.NewTaxRateID(time.Now()), region.ID, base.ID)

	require.Error(t, err, "a stack is a list; a base carries at most one rate above it")
	assert.Contains(t, err.Error(), "tax_rate_stacks_on_uniq")
}

// TestARateCannotCompoundWithNothingUnderIt is the null-safe CHECK.
//
// compound is NOT NULL, so neither side of the constraint's OR can be NULL and
// Postgres cannot satisfy it by ignorance — which is the trap a naive
// "stacks_on_id IS NOT NULL OR NOT compound" would have fallen into had
// compound been nullable.
func TestARateCannotCompoundWithNothingUnderIt(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)

	region, err := svc.CreateTaxRegion(ctx, service.CreateTaxRegionInput{
		CountryCode: benzersizUlke(t),
	})
	require.NoError(t, err)

	_, err = testPool.Pool().Exec(ctx,
		`INSERT INTO tax_rate (id, tax_region_id, name, rate_bps, compound)
         VALUES ($1, $2, 'floating', 100, TRUE)`,
		models.NewTaxRateID(time.Now()), region.ID)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "tax_rate_compound_check")
}
