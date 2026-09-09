//go:build integration

// The tests here run against a real PostgreSQL; to run them:
// make test-integration
//
// What they cover is the SCHEMA's half of the price list's free-form field: the
// default a row written before the column existed gets, and the round trip of a
// document through jsonb. The unit tests prove the service's decisions against a
// fake, and a fake cannot disagree with a default it does not have.
package pricing_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/pricing/models"
	"github.com/bdrtr/gobit/internal/modules/pricing/service"
)

// TestAPriceListCarriesItsMetadataThroughTheDatabase proves the document
// survives the round trip unchanged, nesting included.
func TestAPriceListCarriesItsMetadataThroughTheDatabase(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	written, err := svc.CreatePriceList(ctx, service.PriceListInput{
		Title: "Spring campaign",
		Type:  models.PriceListSale,
		Metadata: map[string]any{
			"campaign": "spring",
			"owner":    map[string]any{"team": "growth"},
		},
	})
	require.NoError(t, err)

	read, err := svc.GetPriceList(ctx, written.ID)
	require.NoError(t, err)

	assert.Equal(t, "spring", read.Metadata["campaign"])
	assert.Equal(t, map[string]any{"team": "growth"}, read.Metadata["owner"],
		"a nested object has to come back as an object, not as a string")
}

// TestAPriceListWithoutMetadataReadsAsAbsent keeps the field out of the response
// entirely rather than present and empty.
//
// The column is NOT NULL and defaults to an empty object, which is also what
// every list written before the column existed holds. Reading that back as an
// empty map would put "metadata": {} on every response for a field nobody
// filled.
func TestAPriceListWithoutMetadataReadsAsAbsent(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	written, err := svc.CreatePriceList(ctx, service.PriceListInput{
		Title: "No metadata", Type: models.PriceListOverride,
	})
	require.NoError(t, err)

	read, err := svc.GetPriceList(ctx, written.ID)
	require.NoError(t, err)
	assert.Nil(t, read.Metadata)

	var stored string
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT metadata::text FROM price_list WHERE id = $1`, written.ID).Scan(&stored))
	assert.JSONEq(t, `{}`, stored,
		"the column is NOT NULL, so what is stored is an empty object")
}

// TestUpdatingAPriceListREPLACESItsMetadata pins the rule the write follows.
//
// Every other field of this body is replaced, and a merged map would leave no
// way to remove a key: a caller could add and change, never delete.
func TestUpdatingAPriceListREPLACESItsMetadata(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	written, err := svc.CreatePriceList(ctx, service.PriceListInput{
		Title:    "Before",
		Type:     models.PriceListSale,
		Metadata: map[string]any{"keep": "no", "drop": "yes"},
	})
	require.NoError(t, err)

	updated, err := svc.UpdatePriceList(ctx, written.ID, service.PriceListInput{
		Title:    "After",
		Type:     models.PriceListSale,
		Metadata: map[string]any{"keep": "yes"},
	})
	require.NoError(t, err)

	assert.Equal(t, map[string]any{"keep": "yes"}, updated.Metadata)
	assert.NotContains(t, updated.Metadata, "drop", "a key left out is a key REMOVED")

	cleared, err := svc.UpdatePriceList(ctx, written.ID, service.PriceListInput{
		Title: "After", Type: models.PriceListSale,
	})
	require.NoError(t, err)
	assert.Nil(t, cleared.Metadata, "an update that sends none clears the field")
}
