package service_test

import (
	"bytes"
	"context"
	"encoding/csv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// later is a moment no test run reaches.
var later = time.Now().Add(time.Hour)

// csvOf writes rows as a CSV file.
func csvOf(t *testing.T, rows ...[]string) []byte {
	t.Helper()

	var out bytes.Buffer
	writer := csv.NewWriter(&out)
	require.NoError(t, writer.WriteAll(rows))

	return out.Bytes()
}

// importAll takes a file and works through it.
func importAll(t *testing.T, svc *service.Service, file []byte) models.Import {
	t.Helper()

	ctx := context.Background()
	created, err := svc.CreateImport(ctx, file)
	require.NoError(t, err)
	_, err = svc.ApplyImports(ctx, later)
	require.NoError(t, err)
	done, err := svc.GetImport(ctx, created.ID)
	require.NoError(t, err)

	return done
}

// TestAnExportComesBackAsAnImport is ADR 0205's round trip: the export's file,
// edited, updates what it names and creates what it does not.
func TestAnExportComesBackAsAnImport(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc := newImportService(t, newMemStore(), newFakeLinker(), &exportGraph{currencies: []string{"TRY"}}, newFakePrices())
	shirt, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle: "shirt", Title: "Shirt", Status: models.StatusPublished,
		Options: []service.CreateOptionInput{{Title: "Size", Values: []string{"S", "M"}}},
		Variants: []service.CreateVariantInput{
			{Title: "Small", SKU: ptr("SHIRT-S"), Options: map[string]string{"Size": "S"}},
			{Title: "Medium", SKU: ptr("SHIRT-M"), Options: map[string]string{"Size": "M"}},
		},
	})
	require.NoError(t, err)

	var exported bytes.Buffer
	require.NoError(t, svc.ExportProducts(ctx, &exported, service.ExportOptions{}, nil))
	records, err := csv.NewReader(&exported).ReadAll()
	require.NoError(t, err)
	header := records[0]
	at := func(column string) int {
		for i, name := range header {
			if name == column {
				return i
			}
		}
		t.Fatalf("no column %q", column)
		return -1
	}
	for _, record := range records[1:] {
		record[at("product_title")] = "Shirt, renamed"
		if record[at("variant_sku")] == "SHIRT-M" {
			record[at("variant_title")] = "Medium, renamed"
		}
	}
	large := make([]string, len(header))
	copy(large, records[1])
	large[at("variant_id")], large[at("variant_sku")] = "", "SHIRT-L"
	large[at("variant_title")], large[at("variant_options")] = "Large", `{"Size":"L"}`
	fresh := make([]string, len(header))
	fresh[at("product_handle")], fresh[at("product_title")] = "scarf", "'=Scarf"
	fresh[at("product_status")] = "draft"
	rows := append(append(records[:1:1], records[1:]...), large, fresh)

	done := importAll(t, svc, csvOf(t, rows...))

	assert.Equal(t, models.ImportCompleted, done.Status)
	assert.Equal(t, 4, done.RowsTotal)
	assert.Equal(t, 4, done.RowsDone)
	assert.Equal(t, 2, done.RowsCreated, "the large variant and the scarf")
	assert.Equal(t, 2, done.RowsUpdated, "the renamed product on its first row, the renamed variant on its own")
	assert.Zero(t, done.RowsFailed, "%v", done.Errors)

	after, err := svc.GetProduct(ctx, shirt.ID)
	require.NoError(t, err)
	assert.Equal(t, "Shirt, renamed", after.Title)
	require.Len(t, after.Variants, 3)
	titles := map[string]string{}
	for _, variant := range after.Variants {
		titles[*variant.SKU] = variant.Title
	}
	assert.Equal(t, "Medium, renamed", titles["SHIRT-M"])
	assert.Equal(t, "Large", titles["SHIRT-L"])

	scarf, err := svc.GetProductByHandle(ctx, "scarf")
	require.NoError(t, err)
	assert.Equal(t, "=Scarf", scarf.Title, "the export's apostrophe comes off again")
	assert.Equal(t, models.StatusDraft, scarf.Status)
}

// TestAFileTheImportCannotReadIsRefusedWhole names what is wrong before a row
// is applied.
func TestAFileTheImportCannotReadIsRefusedWhole(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(), &exportGraph{})
	for name, file := range map[string][]byte{
		"empty":            {},
		"not UTF-8":        []byte("product_handle\n\xff\xfe\n"),
		"no rows":          []byte("product_handle,product_title\n"),
		"unknown column":   []byte("product_handle,product_titel\nshirt,Shirt\n"),
		"column twice":     []byte("product_handle,product_handle\nshirt,shirt\n"),
		"no way to a row":  []byte("product_title\nShirt\n"),
		"a row too narrow": []byte("product_handle,product_title\nshirt\n"),
	} {
		_, err := svc.CreateImport(context.Background(), file)
		require.Error(t, err, name)
		assert.True(t, errors.IsInvalid(err), "%s: %v", name, err)
	}
}

// TestARowThatCannotBeAppliedIsRefusedAlone keeps the rows around it: each
// failed row is counted and named, and the rest are applied.
func TestARowThatCannotBeAppliedIsRefusedAlone(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc := newImportService(t, newMemStore(), newFakeLinker(), &exportGraph{}, newFakePrices())
	_, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle: "shirt", Title: "Shirt",
		Options:  []service.CreateOptionInput{{Title: "Size", Values: []string{"S"}}},
		Variants: []service.CreateVariantInput{{Title: "Small", SKU: ptr("S1"), Options: map[string]string{"Size": "S"}}},
	})
	require.NoError(t, err)

	done := importAll(t, svc, csvOf(t,
		[]string{"product_id", "product_handle", "product_title", "product_discountable", "variant_title", "variant_price_try"},
		[]string{"prod_missing", "", "Ghost", "", "", ""},
		[]string{"", "shirt", "Shirt", "maybe", "", ""},
		[]string{"", "shirt", "", "", "A variant nobody can find again", ""},
		[]string{"", "hat", "Hat", "true", "", ""},
	))

	assert.Equal(t, 4, done.RowsDone)
	assert.Equal(t, 3, done.RowsFailed)
	assert.Equal(t, 1, done.RowsCreated, "the hat")
	require.Len(t, done.Errors, 3)
	assert.Equal(t, []int{2, 3, 4}, []int{done.Errors[0].Row, done.Errors[1].Row, done.Errors[2].Row},
		"a row is named by its line in the file, the header being line 1")
	assert.Contains(t, done.Errors[1].Message, "product_discountable")
	assert.Contains(t, done.Errors[2].Message, "SKU or options")

	_, err = svc.GetProductByHandle(ctx, "hat")
	require.NoError(t, err, "the rows after the refused ones are applied")
}

// TestARowAppliedTwiceFindsWhatItMade is the resumption ADR 0205 relies on: a
// run cut off after applying a row and before recording it runs the row
// again, and the row finds its product and variant instead of making them
// twice.
func TestARowAppliedTwiceFindsWhatItMade(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newMemStore()
	svc := newService(t, store, newFakeLinker(), &exportGraph{})
	file := csvOf(t,
		[]string{"product_handle", "product_title", "variant_title", "variant_sku", "variant_options"},
		[]string{"shirt", "Shirt", "Small", "S1", `{"Size":"S"}`},
		[]string{"shirt", "Shirt", "Medium", "M1", `{"Size":"M"}`},
	)
	created, err := svc.CreateImport(ctx, file)
	require.NoError(t, err)
	_, err = svc.ApplyImports(ctx, later)
	require.NoError(t, err)

	// The run is replayed as if nothing had been recorded.
	store.mu.Lock()
	replay := store.imports[created.ID]
	replay.record.Status, replay.record.RowsDone, replay.file = models.ImportPending, 0, file
	store.mu.Unlock()
	_, err = svc.ApplyImports(ctx, later)
	require.NoError(t, err)

	shirt, err := svc.GetProductByHandle(ctx, "shirt")
	require.NoError(t, err)
	assert.Len(t, shirt.Variants, 2, "no variant made twice")
	listed, err := svc.ListProducts(ctx, service.ListProductsOptions{Limit: 10})
	require.NoError(t, err)
	assert.Len(t, listed.Items, 1, "no product made twice")
}

// TestAnImportResumesWhereItStopped: a run out of time applies nothing more,
// and the next one carries on from the row it reached.
func TestAnImportResumesWhereItStopped(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc := newService(t, newMemStore(), newFakeLinker(), &exportGraph{})
	created, err := svc.CreateImport(ctx, csvOf(t,
		[]string{"product_handle", "product_title"},
		[]string{"a", "A"}, []string{"b", "B"},
	))
	require.NoError(t, err)

	applied, err := svc.ApplyImports(ctx, time.Now().Add(-time.Second))
	require.NoError(t, err)
	assert.Zero(t, applied)
	stopped, err := svc.GetImport(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, models.ImportRunning, stopped.Status)
	assert.Zero(t, stopped.RowsDone)

	applied, err = svc.ApplyImports(ctx, later)
	require.NoError(t, err)
	assert.Equal(t, 2, applied)
	finished, err := svc.GetImport(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, models.ImportCompleted, finished.Status)
	assert.NotNil(t, finished.FinishedAt)
}

// TestAnImportWithNothingToDoIsQuiet: no import, no work, no error.
func TestAnImportWithNothingToDoIsQuiet(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(), &exportGraph{})
	applied, err := svc.ApplyImports(context.Background(), later)

	require.NoError(t, err)
	assert.Zero(t, applied)
}

// TestAnUnchangedExportChangesNothing: importing the export as it came out
// applies every row and counts none as created or updated.
func TestAnUnchangedExportChangesNothing(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc := newImportService(t, newMemStore(), newFakeLinker(), &exportGraph{currencies: []string{"TRY"}}, newFakePrices())
	_, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle: "shirt", Title: "Shirt", Status: models.StatusPublished, Subtitle: ptr("Cotton"),
		Options: []service.CreateOptionInput{{Title: "Size", Values: []string{"S", "M"}}},
		Variants: []service.CreateVariantInput{
			{Title: "Small", SKU: ptr("SHIRT-S"), Options: map[string]string{"Size": "S"}},
			{Title: "Medium", SKU: ptr("SHIRT-M"), Options: map[string]string{"Size": "M"}},
		},
	})
	require.NoError(t, err)
	var exported bytes.Buffer
	require.NoError(t, svc.ExportProducts(ctx, &exported, service.ExportOptions{}, nil))

	done := importAll(t, svc, exported.Bytes())

	assert.Equal(t, 2, done.RowsDone)
	assert.Zero(t, done.RowsCreated)
	assert.Zero(t, done.RowsUpdated, "a row that matches what is there changes nothing")
	assert.Zero(t, done.RowsFailed, "%v", done.Errors)
}

// TestAVariantWithoutOptionsIsFoundByItsSKU: a product with no options has
// nothing to match a variant by but its SKU, and the row updates that variant.
func TestAVariantWithoutOptionsIsFoundByItsSKU(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc := newService(t, newMemStore(), newFakeLinker(), &exportGraph{})
	_, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle: "hat", Title: "Hat",
		Variants: []service.CreateVariantInput{{Title: "One size", SKU: ptr("HAT-1")}},
	})
	require.NoError(t, err)

	done := importAll(t, svc, csvOf(t,
		[]string{"product_handle", "variant_sku", "variant_title"},
		[]string{"hat", "HAT-1", "One size, renamed"},
	))

	assert.Zero(t, done.RowsFailed, "%v", done.Errors)
	assert.Equal(t, 1, done.RowsUpdated)
	hat, err := svc.GetProductByHandle(ctx, "hat")
	require.NoError(t, err)
	require.Len(t, hat.Variants, 1)
	assert.Equal(t, "One size, renamed", hat.Variants[0].Title)
}

// TestARowFindsItsVariantAsTheCatalogDoes: a row's options match a variant's by
// the comparison resolveByTitle makes, so a value the product module takes as
// the same is the same variant here too. strings.EqualFold is not that
// comparison: U+0130, the capital I with a dot, lowers to "i" and does not
// fold to it.
func TestARowFindsItsVariantAsTheCatalogDoes(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc := newService(t, newMemStore(), newFakeLinker(), &exportGraph{})
	_, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle: "print", Title: "Print",
		Options:  []service.CreateOptionInput{{Title: "Edition", Values: []string{"\u0130X", "X"}}},
		Variants: []service.CreateVariantInput{{Title: "Ninth", Options: map[string]string{"Edition": "\u0130X"}}},
	})
	require.NoError(t, err)

	done := importAll(t, svc, csvOf(t,
		[]string{"product_handle", "variant_options", "variant_title"},
		[]string{"print", `{"edition":"ix"}`, "Ninth, renamed"},
	))

	assert.Zero(t, done.RowsFailed, "%v", done.Errors)
	assert.Equal(t, 1, done.RowsUpdated)
	got, err := svc.GetProductByHandle(ctx, "print")
	require.NoError(t, err)
	require.Len(t, got.Variants, 1, "the row found the variant rather than making one")
	assert.Equal(t, "Ninth, renamed", got.Variants[0].Title)
}
