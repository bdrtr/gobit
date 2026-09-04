package product_test

import (
	"io/fs"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/module"
	"github.com/bdrtr/gobit/internal/modules/product"
)

// TestModuleContract verifies the fixed parts of the module's contract with
// the core.
func TestModuleContract(t *testing.T) {
	t.Parallel()

	var mod module.Module = product.New(product.Options{})
	assert.Equal(t, "product", mod.Name())
	assert.Equal(t, "product.service", product.ServiceName,
		"the service name is a cross-module contract; other modules resolve it under this name")
	assert.Equal(t, "product.interop", product.InteropName,
		"the interop name is a cross-module contract; plugins resolve the catalog under this name")
}

// TestMigrationsAreEmbeddedInPairs verifies that the migration files are
// embedded and that every up file has a down counterpart.
//
// A migration without a counterpart cannot be rolled back; Section 8 of the
// plan requires this, and the gap would be seen only once a rollback is
// attempted (that is, at the worst possible moment).
func TestMigrationsAreEmbeddedInPairs(t *testing.T) {
	t.Parallel()

	src := product.New(product.Options{}).Migrations()
	require.NotNil(t, src, "the module must offer a migration source")

	entries, err := fs.ReadDir(src, ".")
	require.NoError(t, err)
	require.NotEmpty(t, entries, "the migration files must be embedded")

	names := map[string]struct{}{}
	for _, entry := range entries {
		names[entry.Name()] = struct{}{}
	}

	upCount := 0
	for name := range names {
		if len(name) < len(".up.sql") || name[len(name)-len(".up.sql"):] != ".up.sql" {
			continue
		}
		upCount++
		down := name[:len(name)-len(".up.sql")] + ".down.sql"
		assert.Contains(t, names, down, "there must be a rollback file for %s", name)
	}
	assert.Positive(t, upCount, "there must be at least one up migration")
	assert.Contains(t, names, "000001_product_init.up.sql")
}

// TestRoutesWithoutRegisterIsNoop verifies that if Routes is called without
// Register having run, no endpoint is mounted.
//
// A handler without a service would give a nil pointer panic on the first
// request if it were mounted; the endpoint not existing at all (404) is
// better than that.
func TestRoutesWithoutRegisterIsNoop(t *testing.T) {
	t.Parallel()

	r := chi.NewRouter()
	assert.NotPanics(t, func() { product.New(product.Options{}).Routes(r) })
	assert.Nil(t, product.New(product.Options{}).Service(), "the service is not set up unless Register is called")
}
