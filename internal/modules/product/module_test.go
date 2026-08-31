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

// TestModuleContract modülün çekirdek sözleşmesinin sabit parçalarını
// doğrular.
func TestModuleContract(t *testing.T) {
	t.Parallel()

	var mod module.Module = product.New()
	assert.Equal(t, "product", mod.Name())
	assert.Equal(t, "product.service", product.ServiceName,
		"servis adı modüller arası sözleşmedir; başka modüller bu adla çözer")
	assert.Equal(t, "product.interop", product.InteropName,
		"interop adı modüller arası sözleşmedir; eklentiler katalogu bu adla çözer")
}

// TestMigrationsAreEmbeddedInPairs migration dosyalarının gömüldüğünü ve her
// up dosyasının bir down eşi olduğunu doğrular.
//
// Eşi olmayan bir migration geri alınamaz; plan Bölüm 8 bunu şart koşar ve
// eksiklik ancak geri alma denendiğinde (yani en kötü anda) görülürdü.
func TestMigrationsAreEmbeddedInPairs(t *testing.T) {
	t.Parallel()

	src := product.New().Migrations()
	require.NotNil(t, src, "modül migration kaynağı sunmalı")

	entries, err := fs.ReadDir(src, ".")
	require.NoError(t, err)
	require.NotEmpty(t, entries, "migration dosyaları gömülmeli")

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
		assert.Contains(t, names, down, "%s için geri alma dosyası olmalı", name)
	}
	assert.Positive(t, upCount, "en az bir up migration bulunmalı")
	assert.Contains(t, names, "000001_product_init.up.sql")
}

// TestRoutesWithoutRegisterIsNoop Register çalışmadan Routes çağrılırsa hiçbir
// uç bağlanmadığını doğrular.
//
// Servisi olmayan bir handler bağlansaydı ilk istekte nil pointer paniği
// verirdi; ucun hiç var olmaması (404) bundan iyidir.
func TestRoutesWithoutRegisterIsNoop(t *testing.T) {
	t.Parallel()

	r := chi.NewRouter()
	assert.NotPanics(t, func() { product.New().Routes(r) })
	assert.Nil(t, product.New().Service(), "Register çağrılmadan servis kurulmaz")
}
