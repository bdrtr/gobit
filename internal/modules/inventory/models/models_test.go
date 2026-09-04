package models_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/inventory/models"
)

// TestAvailable satılabilir adedin stocked - reserved farkı olduğunu doğrular.
//
// Fikstürlerde stocked ve reserved birbirinden farklıdır; farkı almak yerine
// yalnızca birini döndüren bir uygulama hiçbir satırı tutturamaz.
func TestAvailable(t *testing.T) {
	tests := []struct {
		ad       string
		stocked  int64
		reserved int64
		beklenen int64
	}{
		{"rezerve yok", 10, 0, 10},
		{"kısmen rezerve", 10, 4, 6},
		{"tamamı rezerve", 7, 7, 0},
		{"boş stok", 0, 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.ad, func(t *testing.T) {
			level := models.InventoryLevel{StockedQuantity: tt.stocked, ReservedQuantity: tt.reserved}

			assert.Equal(t, tt.beklenen, level.Available())
		})
	}
}

// TestReservationStatusValid yalnızca tanımlı durumların geçerli sayıldığını
// doğrular.
func TestReservationStatusValid(t *testing.T) {
	for _, status := range []models.ReservationStatus{
		models.ReservationActive, models.ReservationReleased, models.ReservationConfirmed,
	} {
		assert.True(t, status.Valid(), "%q geçerli olmalı", status)
	}

	assert.False(t, models.ReservationStatus("").Valid())
	assert.False(t, models.ReservationStatus("iptal").Valid())
}

// TestKimlikOnekleri her varlığın kendi önekini aldığını ve öneklerin
// birbirinden ayırt edilebildiğini doğrular.
func TestKimlikOnekleri(t *testing.T) {
	tests := []struct {
		ad   string
		uret func() string
		onek string
	}{
		{"lokasyon", models.NewStockLocationID, models.StockLocationIDPrefix},
		{"kalem", models.NewInventoryItemID, models.InventoryItemIDPrefix},
		{"seviye", models.NewInventoryLevelID, models.InventoryLevelIDPrefix},
		{"rezervasyon", models.NewReservationID, models.ReservationIDPrefix},
	}

	for _, tt := range tests {
		t.Run(tt.ad, func(t *testing.T) {
			id := tt.uret()

			assert.True(t, strings.HasPrefix(id, tt.onek), "%q, %q ile başlamalı", id, tt.onek)
			assert.Len(t, id, len(tt.onek)+26, "gövde 26 karakter olmalı")
			assert.NotContains(t, strings.TrimPrefix(id, tt.onek), "=", "dolgu karakteri olmamalı")
		})
	}
}

// TestKimlikTekildir aynı milisaniyede üretilen kimliklerin bile
// çakışmadığını doğrular.
func TestKimlikTekildir(t *testing.T) {
	const adet = 2000

	gorulen := make(map[string]struct{}, adet)
	for range adet {
		id := models.NewInventoryItemID()
		_, tekrar := gorulen[id]
		require.False(t, tekrar, "the id repeated: %s", id)
		gorulen[id] = struct{}{}
	}
}

// TestKimlikZamanaGoreSiralanir sonra üretilen kimliğin sözlüksel olarak da
// sonra geldiğini doğrular.
//
// Sıralanabilirlik plan Bölüm 8'in şartıdır ve bedava değildir: zaman damgası
// gövdenin BAŞINDA ve kodlama sırayı koruyan bir alfabede olmalıdır. Rastgele
// bir kimlik bu testi geçemez.
func TestKimlikZamanaGoreSiralanir(t *testing.T) {
	const adet = 8

	idler := make([]string, 0, adet)
	for range adet {
		idler = append(idler, models.NewReservationID())
		// Milisaniye çözünürlüğündeki damganın ilerlemesi için yeterli.
		time.Sleep(2 * time.Millisecond)
	}

	assert.True(t, slices.IsSorted(idler), "kimlikler üretim sırasında sıralı olmalı: %v", idler)
}
