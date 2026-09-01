//go:build integration

// Bu dosyadaki test gerçek bir PostgreSQL örneği (dolayısıyla Docker) gerektirir.
// Çalıştırmak için: make test-integration
package arch_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/internal/core/db"
)

const postgresImage = "postgres:16-alpine"

// TestMigrationlarGercektenGeriAlinabilir her modülün migration'larını GERÇEK
// PostgreSQL'de up -> down -> up çalıştırır.
//
// Neden ayrı bir kapı: [TestMigrationlarGeriAlinabilir] yalnızca .down.sql
// dosyasının VAR OLDUĞUNU denetler. Faz 5'te tam o açıktan bir hata geçti —
// region modülünün tohum geri alması sözdizimsel olarak duruyordu ama
// çalıştırıldığında foreign key ihlaliyle patlıyor, golang-migrate'in sürüm
// defterini "dirty" bırakıyordu. cmd/server her açılışta modül başına Migrate
// çağırdığı için o noktadan sonra sunucu bir daha AÇILMIYORDU.
//
// Migration kaynakları modüllerin embed.FS'lerinden değil DOSYA SİSTEMİNDEN
// okunur: böylece yeni bir modül eklendiğinde bu testin güncellenmesi gerekmez
// ve arch paketi modülleri import etmek zorunda kalmaz.
//
// SINIR: bu kapı VERİYE BAĞLI geri alma hatalarını yakalamaz. Yukarıdaki region
// hatası ancak tabloda bir bölge satırı varken ortaya çıkıyordu; öyle senaryolar
// ilgili modülün kendi entegrasyon testinde kurulmalıdır.
func TestMigrationlarGercektenGeriAlinabilir(t *testing.T) {
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx, postgresImage,
		tcpostgres.WithDatabase("gobit_arch"),
		tcpostgres.WithUsername("gobit"),
		tcpostgres.WithPassword("gobit"),
		tcpostgres.BasicWaitStrategies(),
	)
	t.Cleanup(func() {
		if termErr := testcontainers.TerminateContainer(ctr); termErr != nil {
			t.Logf("postgres konteyneri durdurulamadı: %v", termErr)
		}
	})
	require.NoError(t, err, "postgres konteyneri başlatılamadı")

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	kosulan := 0

	for _, mod := range modulNames(t) {
		migDir := filepath.Join(repoRoot, modulesDir, mod, migrationDizinAdi)
		entries, globErr := filepath.Glob(filepath.Join(migDir, "*.up.sql"))
		require.NoError(t, globErr)
		if len(entries) == 0 {
			continue
		}
		kosulan++

		t.Run(mod, func(t *testing.T) {
			src := os.DirFS(migDir)

			require.NoError(t, db.Migrate(ctx, dsn, src, mod), "ilk up başarısız")

			version, dirty, verErr := db.Version(ctx, dsn, mod)
			require.NoError(t, verErr)
			require.False(t, dirty, "up sonrası şema dirty")
			require.Positive(t, version, "up sonrası sürüm sıfır kaldı")

			// Tamamen geri al. Patlayan bir down sürüm defterini dirty bırakır
			// ve modülü kalıcı olarak açılamaz hâle getirir.
			require.NoError(t, db.MigrateDown(ctx, dsn, src, mod, 0),
				"down başarısız — bu, modülün bir daha migrate EDİLEMEMESİ demektir")

			_, dirty, verErr = db.Version(ctx, dsn, mod)
			require.NoError(t, verErr)
			require.False(t, dirty, "down sonrası şema dirty kaldı")

			// Tekrar up: down'ın şemayı gerçekten temizlediğini kanıtlar.
			// Kalıntı bir tablo bırakan down burada "already exists" ile patlar.
			require.NoError(t, db.Migrate(ctx, dsn, src, mod), "down sonrası tekrar up başarısız")

			after, dirty, verErr := db.Version(ctx, dsn, mod)
			require.NoError(t, verErr)
			assert.False(t, dirty)
			assert.Equal(t, version, after, "gidiş-dönüş sonrası sürüm değişti")

			// Bir sonraki modül temiz bir zeminde başlasın.
			require.NoError(t, db.MigrateDown(ctx, dsn, src, mod, 0))
		})
	}

	// Bu testin sessizce boşalması diğerlerinden PAHALIDIR: koşu bir konteyner
	// açar, dakikalar sürer ve sonunda "ok" yazar — yani hem bir şey yaptığı
	// izlenimi verir hem de hiçbir migration çalıştırmamış olur. Boş bir liste
	// atlanacak bir durum değil, teşhis edilecek bir arızadır.
	require.Positive(t, kosulan,
		"hiçbir modülde çalıştırılacak migration bulunamadı; denetim KÖR kalmış olmalı — "+
			"dosyalar %q dizini dışına taşınmış ya da adlandırma \".up.sql\" kalıbından "+
			"çıkmış olabilir.\nGerçek veritabanında hiç up/down koşmayan bir kapı, "+
			"golang-migrate defterini \"dirty\" bırakan bir down'ı yakalayamaz — Faz 5'te "+
			"sunucuyu açılamaz hâle getiren arıza tam olarak buydu.", migrationDizinAdi)
}
