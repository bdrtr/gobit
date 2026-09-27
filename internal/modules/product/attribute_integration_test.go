//go:build integration

package product_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/link"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/repository"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// attributeWorld is three published products in a collection of their own, and
// three attributes whose handles no other test uses.
type attributeWorld struct {
	svc                         *service.Service
	collection                  string
	material, width, waterproof string
	depth                       string
	shirt, jacket, basket       models.Product
}

func newAttributeWorld(t *testing.T) attributeWorld {
	t.Helper()

	ctx := context.Background()
	links := link.New(testPool, nil)
	for _, def := range service.Definitions() {
		require.NoError(t, links.Define(ctx, def))
	}
	svc := newService(t, links, nil)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	w := attributeWorld{
		svc: svc, material: "material-" + suffix, width: "width-" + suffix, waterproof: "waterproof-" + suffix,
		depth: "depth-" + suffix,
	}
	_, err := svc.CreateAttribute(ctx, service.AttributeInput{Handle: w.material, Title: "Material",
		Kind: models.AttributeSelect, Options: []service.AttributeOptionInput{{Value: "Cotton"}, {Value: "Wool"}, {Value: "Linen"}}})
	require.NoError(t, err)
	_, err = svc.CreateAttribute(ctx, service.AttributeInput{Handle: w.width, Title: "Width", Kind: models.AttributeNumber})
	require.NoError(t, err)
	_, err = svc.CreateAttribute(ctx, service.AttributeInput{Handle: w.waterproof, Title: "Waterproof", Kind: models.AttributeBoolean})
	require.NoError(t, err)
	_, err = svc.CreateAttribute(ctx, service.AttributeInput{Handle: w.depth, Title: "Depth", Kind: models.AttributeNumber})
	require.NoError(t, err)

	collection, err := svc.CreateCollection(ctx, service.CreateCollectionInput{Title: "Attributes", Handle: uniqueHandle("attributes")})
	require.NoError(t, err)
	w.collection = collection.ID
	product := func(name string) models.Product {
		p, err := svc.CreateProduct(ctx, service.CreateProductInput{
			Handle: uniqueHandle(name), Title: name, Status: models.StatusPublished, CollectionID: &w.collection,
			Variants: []service.CreateVariantInput{{Title: "One size"}},
		})
		require.NoError(t, err)
		return p
	}
	w.shirt, w.jacket, w.basket = product("shirt"), product("jacket"), product("basket")
	yes, no := true, false
	fifty, seventy, thirty, deep := 50.0, 70.0, 30.5, 200.0
	for id, values := range map[string][]service.ProductAttributeInput{
		w.shirt.ID: {
			{Attribute: w.material, Options: []string{"cotton"}}, {Attribute: w.width, Number: &fifty},
			{Attribute: w.waterproof, Boolean: &no}, {Attribute: w.depth, Number: &deep},
		},
		w.jacket.ID: {
			{Attribute: w.material, Options: []string{"wool", "cotton"}}, {Attribute: w.width, Number: &seventy},
			{Attribute: w.waterproof, Boolean: &yes},
		},
		w.basket.ID: {{Attribute: w.width, Number: &thirty}},
	} {
		_, err := svc.SetProductAttributes(ctx, id, values)
		require.NoError(t, err)
	}
	return w
}

// listed returns the ids of the collection's products the criteria keep.
func (w attributeWorld) listed(t *testing.T, criteria ...service.AttributeCriterion) []string {
	t.Helper()

	result, err := w.svc.ListProducts(context.Background(), service.ListProductsOptions{
		CollectionID: &w.collection, Attributes: criteria, Limit: 50,
	})
	require.NoError(t, err)
	var out []string
	for i := range result.Items {
		out = append(out, result.Items[i].ID)
	}
	return out
}

// facet finds one attribute's facet.
func facet(t *testing.T, facets []service.Facet, handle string) service.Facet {
	t.Helper()

	for i := range facets {
		if facets[i].Handle == handle {
			return facets[i]
		}
	}
	require.FailNow(t, "no facet", handle)
	return service.Facet{}
}

// TestAttributesFilterAndCountOnTheRealSchema is ADR 0219 in SQL: options
// ORed within an attribute and attributes ANDed, a range with its bounds, a
// product holding two named options listed once, the facets counting the
// filtered attribute without its own filter and the rest within every filter,
// and a product's values read back in the definitions' order.
func TestAttributesFilterAndCountOnTheRealSchema(t *testing.T) {
	w := newAttributeWorld(t)
	ctx := context.Background()
	fifty, sixty, oneFifty := 50.0, 60.0, 150.0

	assert.ElementsMatch(t, []string{w.shirt.ID, w.jacket.ID},
		w.listed(t, service.AttributeCriterion{Attribute: w.material, Options: []string{"cotton", "wool"}}),
		"the jacket holds both and comes back once")
	assert.ElementsMatch(t, []string{w.shirt.ID, w.basket.ID},
		w.listed(t, service.AttributeCriterion{Attribute: w.width, Max: &fifty}), "an inclusive upper bound")
	assert.ElementsMatch(t, []string{w.shirt.ID, w.jacket.ID},
		w.listed(t, service.AttributeCriterion{Attribute: w.width, Min: &fifty}), "an inclusive lower bound")
	assert.Empty(t, w.listed(t, service.AttributeCriterion{Attribute: w.width, Min: &oneFifty}),
		"the shirt's depth is 200, and a range is on the attribute it names")
	assert.ElementsMatch(t, []string{w.jacket.ID},
		w.listed(t, service.AttributeCriterion{Attribute: w.material, Options: []string{"cotton"}},
			service.AttributeCriterion{Attribute: w.width, Min: &sixty}))
	assert.ElementsMatch(t, []string{w.jacket.ID},
		w.listed(t, service.AttributeCriterion{Attribute: w.waterproof, Options: []string{"true"}}))

	facets, err := w.svc.StoreFacets(ctx, service.StoreListOptions{
		CollectionID: &w.collection,
		Attributes:   []service.AttributeCriterion{{Attribute: w.material, Options: []string{"cotton"}}},
	})
	require.NoError(t, err)
	material := facet(t, facets, w.material)
	assert.Equal(t, []int64{2, 0, 1},
		[]int64{material.Options[0].Products, material.Options[1].Products, material.Options[2].Products},
		"cotton 2, linen 0 and wool 1 -- the options in rank then handle order -- counted without "+
			"the material filter")
	width := facet(t, facets, w.width)
	assert.Equal(t, int64(2), width.Products, "within the cotton filter the basket is out")
	assert.Equal(t, []float64{50, 70}, []float64{*width.Min, *width.Max})
	waterproof := facet(t, facets, w.waterproof)
	assert.Equal(t, []int64{1, 1}, []int64{waterproof.True, waterproof.False})

	product, err := w.svc.GetProduct(ctx, w.jacket.ID)
	require.NoError(t, err)
	require.Len(t, product.Attributes, 3, "the jacket holds no depth")
	assert.Equal(t, w.material, product.Attributes[0].Handle)
	assert.Len(t, product.Attributes[0].Options, 2)
}

// TestTheFacetsCountTheChannelsCatalog: a product assigned to another channel
// is out of this channel's facets as it is out of its listing.
func TestTheFacetsCountTheChannelsCatalog(t *testing.T) {
	w := newAttributeWorld(t)
	ctx := context.Background()
	require.NoError(t, w.svc.AddProductSalesChannel(ctx, w.jacket.ID, "sc_elsewhere"))

	facets, err := w.svc.StoreFacets(ctx, service.StoreListOptions{
		CollectionID: &w.collection, SalesChannelIDs: []string{"sc_here"},
	})

	require.NoError(t, err)
	material := facet(t, facets, w.material)
	assert.Equal(t, int64(0), material.Options[2].Products, "the wool jacket is in another channel")
	assert.Equal(t, int64(1), material.Options[0].Products)
}

// TestARemovedOptionAndAttributeNameNothing: a product that chose a removed
// option no longer carries it, a filter naming it is refused, and a removed
// attribute leaves the products and the facets.
func TestARemovedOptionAndAttributeNameNothing(t *testing.T) {
	w := newAttributeWorld(t)
	ctx := context.Background()
	attributes, err := w.svc.ListAttributes(ctx)
	require.NoError(t, err)
	var wool, widthID string
	for i := range attributes {
		switch attributes[i].Handle {
		case w.material:
			for _, o := range attributes[i].Options {
				if o.Handle == "wool" {
					wool = o.ID
				}
			}
		case w.width:
			widthID = attributes[i].ID
		}
	}

	require.NoError(t, w.svc.DeleteAttributeOption(ctx, wool))
	product, err := w.svc.GetProduct(ctx, w.jacket.ID)
	require.NoError(t, err)
	assert.Len(t, product.Attributes[0].Options, 1, "wool is gone, cotton stays")
	_, err = w.svc.ListProducts(ctx, service.ListProductsOptions{
		CollectionID: &w.collection, Attributes: []service.AttributeCriterion{{Attribute: w.material, Options: []string{"wool"}}},
	})
	assert.True(t, errors.IsInvalid(err), "a removed option is refused, not answered with nothing")

	require.NoError(t, w.svc.DeleteAttribute(ctx, widthID))
	product, err = w.svc.GetProduct(ctx, w.basket.ID)
	require.NoError(t, err)
	assert.Empty(t, product.Attributes, "the basket held only the width")
	facets, err := w.svc.StoreFacets(ctx, service.StoreListOptions{CollectionID: &w.collection})
	require.NoError(t, err)
	for i := range facets {
		assert.NotEqual(t, w.width, facets[i].Handle)
	}
	assert.Equal(t, int64(2), facet(t, facets, w.material).Products,
		"the shirt's and the jacket's cotton; the jacket's wool is gone and not counted")
}

// TestOptionsFollowTheirRankAndValuesAreReplaced: an option's rank comes
// before its handle, and a second write of a product's values replaces the
// first.
func TestOptionsFollowTheirRankAndValuesAreReplaced(t *testing.T) {
	w := newAttributeWorld(t)
	ctx := context.Background()
	size := "size-" + fmt.Sprint(time.Now().UnixNano())
	created, err := w.svc.CreateAttribute(ctx, service.AttributeInput{Handle: size, Title: "Size", Kind: models.AttributeSelect,
		Options: []service.AttributeOptionInput{{Value: "Small", Rank: 1}, {Value: "Large", Rank: 2}}})
	require.NoError(t, err)
	attributes, err := w.svc.ListAttributes(ctx)
	require.NoError(t, err)
	found := false
	for i := range attributes {
		if attributes[i].ID == created.ID {
			found = true
			assert.Equal(t, "small", attributes[i].Options[0].Handle, "rank 1 before rank 2, whatever the letters")
		}
	}
	require.True(t, found)

	sixty := 60.0
	_, err = w.svc.SetProductAttributes(ctx, w.shirt.ID, []service.ProductAttributeInput{{Attribute: w.width, Number: &sixty}})
	require.NoError(t, err)
	product, err := w.svc.GetProduct(ctx, w.shirt.ID)
	require.NoError(t, err)
	require.Len(t, product.Attributes, 1, "the second write replaced the first")
	assert.Equal(t, 60.0, *product.Attributes[0].Number)
}

// TestASelectHoldsAtMostTwoHundredOptions: the option count is read under the
// attribute's row lock.
func TestASelectHoldsAtMostTwoHundredOptions(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)
	options := make([]service.AttributeOptionInput, models.MaxAttributeOptions-1)
	for i := range options {
		options[i] = service.AttributeOptionInput{Value: fmt.Sprintf("Option %d", i)}
	}
	a, err := svc.CreateAttribute(ctx, service.AttributeInput{Handle: "many-" + fmt.Sprint(time.Now().UnixNano()),
		Title: "Many", Kind: models.AttributeSelect, Options: options})
	require.NoError(t, err)
	t.Cleanup(func() { _ = svc.DeleteAttribute(context.Background(), a.ID) })

	_, err = svc.AddAttributeOption(ctx, a.ID, service.AttributeOptionInput{Value: "The last"})
	require.NoError(t, err, "the two hundredth")
	_, err = svc.AddAttributeOption(ctx, a.ID, service.AttributeOptionInput{Value: "One too many"})
	require.Error(t, err)
	assert.Equal(t, "product_attribute_limit_reached", errors.CodeOf(err))
}

// TestTheAttributeSchemaRefuses: a value of two kinds, an option of another
// attribute, an option chosen twice, a number that is not finite and a kind the
// schema does not know.
func TestTheAttributeSchemaRefuses(t *testing.T) {
	w := newAttributeWorld(t)
	ctx := context.Background()
	attributes, err := w.svc.ListAttributes(ctx)
	require.NoError(t, err)
	var materialID, cotton, widthID string
	for i := range attributes {
		switch attributes[i].Handle {
		case w.material:
			materialID, cotton = attributes[i].ID, attributes[i].Options[0].ID
		case w.width:
			widthID = attributes[i].ID
		}
	}

	for constraint, statement := range map[string]struct {
		sql  string
		args []any
	}{
		"product_attribute_value_one": {sql: `INSERT INTO product_attribute_value (product_id, attribute_id, number_value, boolean_value)
            VALUES ($1, $2, 1, true)`, args: []any{w.basket.ID, widthID}},
		"product_attribute_value_option_fk": {sql: `INSERT INTO product_attribute_value (product_id, attribute_id, option_id)
            VALUES ($1, $2, $3)`, args: []any{w.basket.ID, widthID, cotton}},
		"product_attribute_value_option_uniq": {sql: `INSERT INTO product_attribute_value (product_id, attribute_id, option_id)
            VALUES ($1, $2, $3)`, args: []any{w.shirt.ID, materialID, cotton}},
		"product_attribute_value_finite": {sql: `INSERT INTO product_attribute_value (product_id, attribute_id, number_value)
            VALUES ($1, $2, 'Infinity')`, args: []any{w.shirt.ID, widthID}},
		"product_attribute_kind_check": {sql: `INSERT INTO product_attribute (id, handle, title, kind)
            VALUES ($1, $1, 'Shade', 'text')`, args: []any{"pattr-" + fmt.Sprint(time.Now().UnixNano())}},
	} {
		_, err := testPool.Pool().Exec(ctx, statement.sql, statement.args...)
		var pgErr *pgconn.PgError
		require.ErrorAs(t, err, &pgErr, constraint)
		assert.Equal(t, constraint, pgErr.ConstraintName)
	}
}

// TestTheAttributeCountWaitsForAWriterThatHasNotCommitted: a second
// transaction takes the count's lock as the repository does and defines the
// last attribute there is room for; a definition asked for meanwhile is judged
// after its commit and refused.
func TestTheAttributeCountWaitsForAWriterThatHasNotCommitted(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)
	var existing int
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM product_attribute WHERE deleted_at IS NULL`).Scan(&existing))
	var made []string
	t.Cleanup(func() {
		for _, id := range made {
			_ = svc.DeleteAttribute(context.Background(), id)
		}
	})
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	for i := range models.MaxAttributes - existing - 1 {
		a, err := svc.CreateAttribute(ctx, service.AttributeInput{
			Handle: fmt.Sprintf("fill-%d-%s", i, suffix), Title: "Fill", Kind: models.AttributeNumber,
		})
		require.NoError(t, err)
		made = append(made, a.ID)
	}
	heldID := "pattr_held" + suffix
	made = append(made, heldID)

	tx, err := testPool.Pool().Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(context.Background()) }()
	_, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, repository.AttributeCountLockKey)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `INSERT INTO product_attribute (id, handle, title, kind) VALUES ($1, $2, 'Held', 'number')`,
		heldID, "held-"+suffix)
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() {
		a, err := svc.CreateAttribute(ctx, service.AttributeInput{Handle: "late-" + suffix, Title: "Late", Kind: models.AttributeNumber})
		if err == nil {
			made = append(made, a.ID)
		}
		done <- err
	}()
	var late error
	finished := false
	select {
	case late = <-done:
		finished = true
	case <-time.After(500 * time.Millisecond):
	}
	require.NoError(t, tx.Commit(ctx))
	if !finished {
		late = <-done
	}

	require.Error(t, late)
	assert.Equal(t, "product_attribute_limit_reached", errors.CodeOf(late))
	var total int
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM product_attribute WHERE deleted_at IS NULL`).Scan(&total))
	assert.Equal(t, models.MaxAttributes, total)
}
