//go:build integration

// Bu dosyadaki testler gerçek bir PostgreSQL örneği (dolayısıyla Docker)
// gerektirir; `make test` hızlı kalsın diye `integration` etiketiyle
// ayrılmıştır. Çalıştırmak için: make test-integration
//
// Buradaki iddiaların çoğu YALNIZCA gerçek veritabanında kanıtlanabilir:
// kısmi benzersiz indeksin iki eşzamanlı Create'ten yalnızca birini geçirdiği,
// ON CONFLICT'in aynı adımı güncellediği, JSONB'nin NULL ile JSON null'ı ayırt
// ettiği ve migration'ın geri alınabildiği birim testinde gösterilemez.
//
// Dosya pgstore paketinin İÇİNDEDİR: hata eşlemesinin dayandığı kısıt adları
// (idempotencyIndex, executionsPKConstraint) dışa açık değildir ve şemayla
// gerçekten örtüştükleri ancak buradan katalog okunarak doğrulanabilir.
package pgstore

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/internal/core/db"
	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/workflow"
)

const postgresImage = "postgres:16-alpine"

var (
	// testPool tüm testlerin paylaştığı havuzdur.
	testPool *db.Pool
	// testDSN aynı veritabanının bağlantı adresidir; migration yolu havuzu
	// değil, kendi bağlantısını kullandığı için ayrıca saklanır.
	testDSN string
	// yonetimDSN yeni veritabanı açmak için kullanılan yönetim adresidir.
	yonetimDSN string
)

func TestMain(m *testing.M) {
	os.Exit(runWithPostgres(m))
}

// runWithPostgres tek bir Postgres konteyneri kaldırır, şemayı uygular ve tüm
// testleri onun üzerinde çalıştırır. os.Exit defer'ları atladığı için ayrı
// fonksiyondadır.
func runWithPostgres(m *testing.M) int {
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx, postgresImage,
		tcpostgres.WithDatabase("gobit_test"),
		tcpostgres.WithUsername("gobit"),
		tcpostgres.WithPassword("gobit"),
		tcpostgres.BasicWaitStrategies(),
	)
	defer func() {
		if termErr := testcontainers.TerminateContainer(ctr); termErr != nil {
			fmt.Fprintf(os.Stderr, "postgres konteyneri durdurulamadı: %v\n", termErr)
		}
	}()
	if err != nil {
		fmt.Fprintf(os.Stderr, "postgres konteyneri başlatılamadı: %v\n", err)
		return 1
	}

	testDSN, err = ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "bağlantı adresi alınamadı: %v\n", err)
		return 1
	}
	yonetimDSN = testDSN

	if err = db.Migrate(ctx, testDSN, Migrations(), MigrationOwner); err != nil {
		fmt.Fprintf(os.Stderr, "workflow şeması uygulanamadı: %v\n", err)
		return 1
	}

	testPool, err = db.New(ctx, db.DefaultConfig(testDSN), nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bağlantı havuzu açılamadı: %v\n", err)
		return 1
	}
	defer testPool.Close()

	return m.Run()
}

// yeniDepo testin kullanacağı depoyu döner.
func yeniDepo() workflow.Store {
	return New(testPool, nil)
}

// wfAdi teste özgü bir workflow adı üretir; testler aynı tabloları paylaştığı
// için birbirlerinin kayıtlarını görmemelidir.
func wfAdi(t *testing.T) string {
	t.Helper()
	ad := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	if len(ad) > maxNameLen {
		ad = ad[:maxNameLen]
	}
	return ad
}

// acilanYurutme testin işleyeceği bir yürütme kaydı açar ve döner.
func acilanYurutme(ctx context.Context, t *testing.T, depo workflow.Store) *workflow.Execution {
	t.Helper()
	exec := &workflow.Execution{
		Workflow: wfAdi(t),
		Status:   workflow.StatusRunning,
		Input:    json.RawMessage(`{"cart_id":"cart_1"}`),
	}
	require.NoError(t, depo.Create(ctx, exec))
	return exec
}

// TestMigrationUpDown şemanın uygulanabildiğini ve GERİ ALINABİLDİĞİNİ
// doğrular (plan Bölüm 8). Kendi veritabanında çalışır: diğer testlerin
// şemasını düşürmek onları etkilerdi.
func TestMigrationUpDown(t *testing.T) {
	ctx := context.Background()
	dsn := yeniVeritabani(ctx, t)

	surum, kirli, err := db.Version(ctx, dsn, MigrationOwner)
	require.NoError(t, err)
	assert.Zero(t, surum, "boş veritabanında sürüm 0 olmalı")
	assert.False(t, kirli)

	require.NoError(t, db.Migrate(ctx, dsn, Migrations(), MigrationOwner))

	assert.True(t, iliskiVar(ctx, t, dsn, "workflow_executions"), "yürütme tablosu oluşmalı")
	assert.True(t, iliskiVar(ctx, t, dsn, "workflow_execution_steps"), "adım tablosu oluşmalı")
	assert.True(t, iliskiVar(ctx, t, dsn, idempotencyIndex), "kısmi benzersiz indeks oluşmalı")

	surum, kirli, err = db.Version(ctx, dsn, MigrationOwner)
	require.NoError(t, err)
	assert.Equal(t, uint(1), surum)
	assert.False(t, kirli)

	require.NoError(t, db.MigrateDown(ctx, dsn, Migrations(), MigrationOwner, 0))

	assert.False(t, iliskiVar(ctx, t, dsn, "workflow_executions"), "geri alma tabloyu düşürmeli")
	assert.False(t, iliskiVar(ctx, t, dsn, "workflow_execution_steps"), "geri alma tabloyu düşürmeli")

	surum, _, err = db.Version(ctx, dsn, MigrationOwner)
	require.NoError(t, err)
	assert.Zero(t, surum, "geri alma sonrası sürüm 0 olmalı")
}

// TestKisitAdlariSemayaUyuyor hata eşlemesinin dayandığı kısıt adlarının
// şemada GERÇEKTEN bu adlarla var olduğunu doğrular.
//
// Ad tutmasaydı createError sessizce genel dala düşer, motor idempotent
// tekrarı CodeDuplicateKey ile tanıyamazdı; bu yüzden adlar katalogdan okunur.
func TestKisitAdlariSemayaUyuyor(t *testing.T) {
	ctx := context.Background()

	assert.True(t, iliskiVar(ctx, t, testDSN, idempotencyIndex),
		"%s adlı indeks şemada olmalı", idempotencyIndex)
	assert.True(t, iliskiVar(ctx, t, testDSN, executionsPKConstraint),
		"%s adlı birincil anahtar şemada olmalı", executionsPKConstraint)
}

// TestCreateVeGetUctanUca kaydın açılıp aynen geri okunduğunu doğrular.
func TestCreateVeGetUctanUca(t *testing.T) {
	ctx := context.Background()
	depo := yeniDepo()

	exec := &workflow.Execution{
		Workflow:       wfAdi(t),
		IdempotencyKey: "ord_1",
		Status:         workflow.StatusRunning,
		Input:          json.RawMessage(`{"cart_id":"cart_1","adet":2}`),
	}
	require.NoError(t, depo.Create(ctx, exec))

	assert.True(t, strings.HasPrefix(exec.ID, "wfx_"), "kimlik önekli üretilmeli: %s", exec.ID)
	assert.False(t, exec.CreatedAt.IsZero(), "CreatedAt geri yazılmalı")
	assert.False(t, exec.UpdatedAt.IsZero(), "UpdatedAt geri yazılmalı")
	assert.Equal(t, time.UTC, exec.CreatedAt.Location(), "zamanlar UTC olmalı")

	okunan, err := depo.Get(ctx, exec.ID)
	require.NoError(t, err)

	assert.Equal(t, exec.ID, okunan.ID)
	assert.Equal(t, exec.Workflow, okunan.Workflow)
	assert.Equal(t, "ord_1", okunan.IdempotencyKey)
	assert.Equal(t, workflow.StatusRunning, okunan.Status)
	assert.JSONEq(t, `{"cart_id":"cart_1","adet":2}`, string(okunan.Input))
	assert.Nil(t, okunan.Output, "yazılmayan çıktı NULL kalmalı")
	assert.Empty(t, okunan.Failure)
	assert.Empty(t, okunan.Steps, "adım yazılmadan Steps boş olmalı")
	assert.True(t, okunan.CreatedAt.Equal(exec.CreatedAt))
	assert.Equal(t, time.UTC, okunan.CreatedAt.Location())
}

// TestCreateVerilenKimligiKorur çağıranın ürettiği kimliğin korunduğunu
// doğrular; motor kimliği kendisi üretip Create'e verir.
func TestCreateVerilenKimligiKorur(t *testing.T) {
	ctx := context.Background()
	depo := yeniDepo()

	exec := &workflow.Execution{
		ID:       "wfx_MOTORUNURETTIGI001",
		Workflow: wfAdi(t),
		Status:   workflow.StatusRunning,
	}
	require.NoError(t, depo.Create(ctx, exec))

	assert.Equal(t, "wfx_MOTORUNURETTIGI001", exec.ID)

	okunan, err := depo.Get(ctx, "wfx_MOTORUNURETTIGI001")
	require.NoError(t, err)
	assert.Equal(t, "wfx_MOTORUNURETTIGI001", okunan.ID)
}

// TestCreateAyniKimlikCakismasi aynı kimliğin ikinci kez açılamadığını doğrular.
//
// Hata Invalid'dir, Conflict DEĞİL: motor Conflict'i "bu istek daha önce
// yapıldı" diye okuyup tekrar yoluna gider, oysa kimlik çakışmasında aranacak
// bir idempotency anahtarı yoktur (bkz. createError).
func TestCreateAyniKimlikCakismasi(t *testing.T) {
	ctx := context.Background()
	depo := yeniDepo()

	ilk := &workflow.Execution{ID: "wfx_AYNIKIMLIK0001", Workflow: wfAdi(t)}
	require.NoError(t, depo.Create(ctx, ilk))

	ikinci := &workflow.Execution{ID: "wfx_AYNIKIMLIK0001", Workflow: wfAdi(t)}
	err := depo.Create(ctx, ikinci)

	require.Error(t, err)
	assert.Equal(t, CodeDuplicateID, coreerrors.CodeOf(err))
	assert.Equal(t, coreerrors.KindInvalid, coreerrors.KindOf(err),
		"aynı kimlik girdi hatasıdır: %v", err)
	assert.False(t, coreerrors.IsConflict(err),
		"Conflict yalnızca idempotency çakışmasına ayrılmıştır: %v", err)
}

// TestCreateBoslukAnahtarReddedilir yalnızca boşluktan oluşan idempotency
// anahtarının SESSİZCE anahtarsıza çevrilmediğini doğrular.
//
// Anahtar NULL'a çekilseydi kısmi benzersiz indeks devreye girmez, aynı
// anahtarla ikinci ve üçüncü kayıt sorunsuz açılırdı: çağıranın istediği
// tekrar koruması hiçbir uyarı vermeden kaybolurdu.
func TestCreateBoslukAnahtarReddedilir(t *testing.T) {
	ctx := context.Background()
	depo := yeniDepo()
	ad := wfAdi(t)

	for _, anahtar := range []string{" ", "   ", "\t"} {
		err := depo.Create(ctx, &workflow.Execution{
			Workflow: ad, IdempotencyKey: anahtar, Status: workflow.StatusRunning,
		})

		require.Errorf(t, err, "%q anahtarı reddedilmeli", anahtar)
		assert.True(t, coreerrors.IsInvalid(err), "hata Invalid olmalı: %v", err)
	}

	var adet int
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM workflow_executions WHERE workflow = $1`, ad).Scan(&adet))
	assert.Zero(t, adet, "reddedilen anahtarla kayıt AÇILMAMALI")

	// Okuma yolu da aynı anahtarı reddeder; iki yolun kabul kümesi aynıdır.
	_, err := depo.FindByIdempotencyKey(ctx, ad, " ")
	require.Error(t, err)
	assert.True(t, coreerrors.IsInvalid(err), "arama da Invalid dönmeli: %v", err)
}

// TestCreateIdempotencyCakismasi aynı (workflow, anahtar) çiftinin ikinci kez
// açılamadığını doğrular.
func TestCreateIdempotencyCakismasi(t *testing.T) {
	ctx := context.Background()
	depo := yeniDepo()
	ad := wfAdi(t)

	require.NoError(t, depo.Create(ctx, &workflow.Execution{
		Workflow: ad, IdempotencyKey: "ord_1", Status: workflow.StatusRunning,
	}))

	err := depo.Create(ctx, &workflow.Execution{
		Workflow: ad, IdempotencyKey: "ord_1", Status: workflow.StatusRunning,
	})

	require.Error(t, err)
	assert.True(t, coreerrors.IsConflict(err), "idempotency çakışması Conflict olmalı: %v", err)
	assert.Equal(t, CodeDuplicateKey, coreerrors.CodeOf(err))
}

// TestCreateFarkliWorkflowAyniAnahtar benzersizliğin workflow'a GÖRE
// olduğunu doğrular: aynı anahtar başka bir workflow'da serbesttir.
func TestCreateFarkliWorkflowAyniAnahtar(t *testing.T) {
	ctx := context.Background()
	depo := yeniDepo()

	require.NoError(t, depo.Create(ctx, &workflow.Execution{
		Workflow: wfAdi(t) + "_a", IdempotencyKey: "ord_1",
	}))
	require.NoError(t, depo.Create(ctx, &workflow.Execution{
		Workflow: wfAdi(t) + "_b", IdempotencyKey: "ord_1",
	}))
}

// TestCreateAnahtarsizYurutmelerCakismaz anahtarsız yürütmelerin birbiriyle
// çakışmadığını doğrular. İndeks kısmi olmasaydı ikinci çağrı düşerdi.
func TestCreateAnahtarsizYurutmelerCakismaz(t *testing.T) {
	ctx := context.Background()
	depo := yeniDepo()
	ad := wfAdi(t)

	for i := range 3 {
		exec := &workflow.Execution{Workflow: ad, Status: workflow.StatusRunning}
		require.NoErrorf(t, depo.Create(ctx, exec), "%d. anahtarsız yürütme açılamadı", i)

		okunan, err := depo.Get(ctx, exec.ID)
		require.NoError(t, err)
		assert.Empty(t, okunan.IdempotencyKey, "anahtarsız kayıt boş anahtarla dönmeli")
	}
}

// TestCreateEszamanliYaris iki (ve daha çok) sürecin aynı anahtarla aynı anda
// kayıt açtığı yarışta YALNIZCA BİRİNİN başarılı olduğunu doğrular.
//
// Testin kanıtladığı şey "önce SELECT sonra INSERT"in yetmediğidir: goroutine'ler
// tek bir kapıdan aynı anda salınır ve karar veritabanındaki benzersiz indekse
// bırakılır.
func TestCreateEszamanliYaris(t *testing.T) {
	ctx := context.Background()
	depo := yeniDepo()
	ad := wfAdi(t)

	const yarisan = 8
	kapi := make(chan struct{})
	sonuclar := make([]error, yarisan)
	kimlikler := make([]string, yarisan)

	var wg sync.WaitGroup
	wg.Add(yarisan)
	for i := range yarisan {
		go func() {
			defer wg.Done()
			exec := &workflow.Execution{
				Workflow:       ad,
				IdempotencyKey: "ord_yaris",
				Status:         workflow.StatusRunning,
				Input:          json.RawMessage(fmt.Sprintf(`{"yarisan":%d}`, i)),
			}
			<-kapi // hepsi aynı anda başlasın
			sonuclar[i] = depo.Create(ctx, exec)
			kimlikler[i] = exec.ID
		}()
	}
	close(kapi)
	wg.Wait()

	var basarili, cakisan int
	var kazananID string
	for i, err := range sonuclar {
		switch {
		case err == nil:
			basarili++
			kazananID = kimlikler[i]
		case coreerrors.IsConflict(err):
			cakisan++
			assert.Equal(t, CodeDuplicateKey, coreerrors.CodeOf(err))
		default:
			t.Errorf("%d. yarışan beklenmeyen hata aldı: %v", i, err)
		}
	}

	assert.Equal(t, 1, basarili, "yarıştan tam bir kazanan çıkmalı")
	assert.Equal(t, yarisan-1, cakisan, "kalan herkes Conflict almalı")

	// Veritabanında da tek kayıt olmalı: sayım, kazananın gerçekten tek
	// olduğunu hata sınıflarından bağımsız olarak doğrular.
	var adet int
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM workflow_executions WHERE workflow = $1 AND idempotency_key = $2`,
		ad, "ord_yaris").Scan(&adet))
	assert.Equal(t, 1, adet, "aynı anahtarla tek satır olmalı")

	bulunan, err := depo.FindByIdempotencyKey(ctx, ad, "ord_yaris")
	require.NoError(t, err)
	assert.Equal(t, kazananID, bulunan.ID, "kalıcılaşan kayıt kazananınki olmalı")
}

// TestFindByIdempotencyKey anahtarla okumanın kaydı adımlarıyla döndürdüğünü
// doğrular.
func TestFindByIdempotencyKey(t *testing.T) {
	ctx := context.Background()
	depo := yeniDepo()
	ad := wfAdi(t)

	exec := &workflow.Execution{
		Workflow: ad, IdempotencyKey: "ord_bul", Status: workflow.StatusRunning,
	}
	require.NoError(t, depo.Create(ctx, exec))
	require.NoError(t, depo.AppendStep(ctx, exec.ID, workflow.StepRecord{
		Name: "stok_rezerve", Index: 0, Status: workflow.StepInvoked, Attempts: 1,
	}))

	bulunan, err := depo.FindByIdempotencyKey(ctx, ad, "ord_bul")

	require.NoError(t, err)
	assert.Equal(t, exec.ID, bulunan.ID)
	require.Len(t, bulunan.Steps, 1, "bulunan kayıt adımlarını da taşımalı")
	assert.Equal(t, "stok_rezerve", bulunan.Steps[0].Name)
}

// TestFindByIdempotencyKeyBulunamadi olmayan anahtarın NotFound döndüğünü
// doğrular.
func TestFindByIdempotencyKeyBulunamadi(t *testing.T) {
	ctx := context.Background()
	depo := yeniDepo()

	_, err := depo.FindByIdempotencyKey(ctx, wfAdi(t), "hic_yazilmadi")

	require.Error(t, err)
	assert.True(t, coreerrors.IsNotFound(err), "bulunamayan kayıt NotFound olmalı: %v", err)
	assert.Equal(t, CodeNotFound, coreerrors.CodeOf(err))
}

// TestGetBulunamadi olmayan kimliğin NotFound döndüğünü doğrular.
func TestGetBulunamadi(t *testing.T) {
	ctx := context.Background()
	depo := yeniDepo()

	_, err := depo.Get(ctx, "wfx_HICYAZILMADI0001")

	require.Error(t, err)
	assert.True(t, coreerrors.IsNotFound(err), "bulunamayan kayıt NotFound olmalı: %v", err)
	assert.Equal(t, CodeNotFound, coreerrors.CodeOf(err))
}

// TestAppendStepAyniIndeksiGunceller aynı Index'e ikinci yazımın YENİ SATIR
// AÇMADIĞINI, var olanı güncellediğini doğrular. Retry sırasında adım önce
// invoked, sonra compensated olarak yazılır; iki satır kalsaydı yürütmenin izi
// yanlış okunurdu.
func TestAppendStepAyniIndeksiGunceller(t *testing.T) {
	ctx := context.Background()
	depo := yeniDepo()
	exec := acilanYurutme(ctx, t, depo)

	baslangic := time.Now().UTC().Truncate(time.Microsecond)
	require.NoError(t, depo.AppendStep(ctx, exec.ID, workflow.StepRecord{
		Name:      "stok_rezerve",
		Index:     0,
		Status:    workflow.StepInvoked,
		Output:    json.RawMessage(`{"rezervasyon":"rez_1"}`),
		Attempts:  1,
		StartedAt: baslangic,
		EndedAt:   baslangic.Add(time.Second),
	}))

	bitis := baslangic.Add(2 * time.Second)
	require.NoError(t, depo.AppendStep(ctx, exec.ID, workflow.StepRecord{
		Name:      "stok_rezerve",
		Index:     0,
		Status:    workflow.StepCompensated,
		Output:    nil,
		Failure:   "stok yetersiz",
		Attempts:  3,
		StartedAt: baslangic,
		EndedAt:   bitis,
	}))

	okunan, err := depo.Get(ctx, exec.ID)
	require.NoError(t, err)

	require.Len(t, okunan.Steps, 1, "ikinci yazım yeni satır eklememeli")
	adim := okunan.Steps[0]
	assert.Equal(t, workflow.StepCompensated, adim.Status, "durum güncellenmeli")
	assert.Equal(t, 3, adim.Attempts, "deneme sayısı güncellenmeli")
	assert.Equal(t, "stok yetersiz", adim.Failure)
	assert.Nil(t, adim.Output, "nil çıktı NULL'a çekilmeli")
	assert.True(t, adim.EndedAt.Equal(bitis), "bitiş zamanı güncellenmeli")
	assert.Equal(t, time.UTC, adim.EndedAt.Location())
}

// TestAppendStepZamanlariKorur adım zamanlarının UTC olarak ve mikrosaniye
// duyarlığında geri okunduğunu, sıfır zamanların sıfır kaldığını doğrular.
func TestAppendStepZamanlariKorur(t *testing.T) {
	ctx := context.Background()
	depo := yeniDepo()
	exec := acilanYurutme(ctx, t, depo)

	yer := time.FixedZone("UTC+3", 3*60*60)
	basladi := time.Date(2026, 8, 23, 15, 4, 5, 123456000, yer)

	require.NoError(t, depo.AppendStep(ctx, exec.ID, workflow.StepRecord{
		Name: "zamanli", Index: 0, Status: workflow.StepInvoked, Attempts: 1,
		StartedAt: basladi,
	}))

	okunan, err := depo.Get(ctx, exec.ID)
	require.NoError(t, err)
	require.Len(t, okunan.Steps, 1)

	adim := okunan.Steps[0]
	assert.True(t, adim.StartedAt.Equal(basladi), "aynı an geri okunmalı")
	assert.Equal(t, time.UTC, adim.StartedAt.Location(), "zaman UTC'ye taşınmalı")
	assert.True(t, adim.EndedAt.IsZero(), "yazılmayan zaman sıfır kalmalı")
}

// TestAppendStepSiraliDoner adımların Index sırasına göre döndüğünü doğrular;
// kayıtlar bilinçli olarak KARIŞIK sırada yazılır.
func TestAppendStepSiraliDoner(t *testing.T) {
	ctx := context.Background()
	depo := yeniDepo()
	exec := acilanYurutme(ctx, t, depo)

	for _, index := range []int{4, 0, 3, 1, 2} {
		require.NoError(t, depo.AppendStep(ctx, exec.ID, workflow.StepRecord{
			Name:     fmt.Sprintf("adim_%d", index),
			Index:    index,
			Status:   workflow.StepInvoked,
			Attempts: 1,
		}))
	}

	okunan, err := depo.Get(ctx, exec.ID)
	require.NoError(t, err)

	require.Len(t, okunan.Steps, 5)
	for i, adim := range okunan.Steps {
		assert.Equal(t, i, adim.Index, "%d. sırada Index %d beklenir", i, i)
		assert.Equal(t, fmt.Sprintf("adim_%d", i), adim.Name)
	}
}

// TestAppendStepYurutmeyiTazeler adım yazmanın yürütmenin UpdatedAt'ini
// ilerlettiğini, CreatedAt'i ise bozmadığını doğrular.
func TestAppendStepYurutmeyiTazeler(t *testing.T) {
	ctx := context.Background()
	depo := yeniDepo()
	exec := acilanYurutme(ctx, t, depo)

	require.NoError(t, depo.AppendStep(ctx, exec.ID, workflow.StepRecord{
		Name: "adim", Index: 0, Status: workflow.StepInvoked, Attempts: 1,
	}))

	okunan, err := depo.Get(ctx, exec.ID)
	require.NoError(t, err)
	assert.True(t, okunan.UpdatedAt.After(exec.UpdatedAt),
		"adım yazımı UpdatedAt'i ilerletmeli (önce %s, sonra %s)", exec.UpdatedAt, okunan.UpdatedAt)
	assert.True(t, okunan.CreatedAt.Equal(exec.CreatedAt), "CreatedAt değişmemeli")
}

// TestAppendStepOlmayanYurutme sahipsiz adım yazımının NotFound döndüğünü
// doğrular; yabancı anahtar bunu veritabanı düzeyinde engeller.
func TestAppendStepOlmayanYurutme(t *testing.T) {
	ctx := context.Background()
	depo := yeniDepo()

	err := depo.AppendStep(ctx, "wfx_OLMAYANYURUTME01", workflow.StepRecord{
		Name: "adim", Index: 0, Status: workflow.StepInvoked, Attempts: 1,
	})

	require.Error(t, err)
	assert.True(t, coreerrors.IsNotFound(err), "olmayan yürütme NotFound olmalı: %v", err)

	var adet int
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM workflow_execution_steps WHERE execution_id = $1`,
		"wfx_OLMAYANYURUTME01").Scan(&adet))
	assert.Zero(t, adet, "sahipsiz adım yazılmamalı")
}

// TestUpdateStatus son durumun ve çıktının yazıldığını doğrular.
func TestUpdateStatus(t *testing.T) {
	ctx := context.Background()
	depo := yeniDepo()
	exec := acilanYurutme(ctx, t, depo)

	require.NoError(t, depo.UpdateStatus(ctx, exec.ID,
		workflow.StatusCompleted, json.RawMessage(`{"order_id":"ord_9"}`), ""))

	okunan, err := depo.Get(ctx, exec.ID)
	require.NoError(t, err)
	assert.Equal(t, workflow.StatusCompleted, okunan.Status)
	assert.JSONEq(t, `{"order_id":"ord_9"}`, string(okunan.Output))
	assert.Empty(t, okunan.Failure)
	assert.True(t, okunan.UpdatedAt.After(exec.UpdatedAt), "UpdatedAt ilerlemeli")
	assert.True(t, okunan.CreatedAt.Equal(exec.CreatedAt), "CreatedAt değişmemeli")
}

// TestUpdateStatusTelafiHatasi elle müdahale isteyen durumun ve arıza
// açıklamasının kalıcılaştığını doğrular.
func TestUpdateStatusTelafiHatasi(t *testing.T) {
	ctx := context.Background()
	depo := yeniDepo()
	exec := acilanYurutme(ctx, t, depo)

	require.NoError(t, depo.UpdateStatus(ctx, exec.ID,
		workflow.StatusCompensationFailed, nil, "telafi patladı: ödeme iadesi başarısız"))

	okunan, err := depo.Get(ctx, exec.ID)
	require.NoError(t, err)
	assert.Equal(t, workflow.StatusCompensationFailed, okunan.Status)
	assert.Equal(t, "telafi patladı: ödeme iadesi başarısız", okunan.Failure)
	assert.Nil(t, okunan.Output, "nil çıktı NULL kalmalı")
}

// TestUpdateStatusOlmayanYurutme olmayan kaydın güncellenemediğini doğrular.
func TestUpdateStatusOlmayanYurutme(t *testing.T) {
	ctx := context.Background()
	depo := yeniDepo()

	err := depo.UpdateStatus(ctx, "wfx_OLMAYANYURUTME02", workflow.StatusCompleted, nil, "")

	require.Error(t, err)
	assert.True(t, coreerrors.IsNotFound(err), "olmayan yürütme NotFound olmalı: %v", err)
	assert.Equal(t, CodeNotFound, coreerrors.CodeOf(err))
}

// TestJSONNULLVeJSONNullAyrimi "değer yok" (SQL NULL) ile "değer null"ın
// (JSON null) hem yazma hem okuma yönünde ayrıldığını doğrular.
func TestJSONNULLVeJSONNullAyrimi(t *testing.T) {
	ctx := context.Background()
	depo := yeniDepo()
	ad := wfAdi(t)

	nilExec := &workflow.Execution{Workflow: ad, Input: nil}
	require.NoError(t, depo.Create(ctx, nilExec))

	nullExec := &workflow.Execution{Workflow: ad, Input: json.RawMessage(`null`)}
	require.NoError(t, depo.Create(ctx, nullExec))

	bosExec := &workflow.Execution{Workflow: ad, Input: json.RawMessage{}}
	require.NoError(t, depo.Create(ctx, bosExec))

	okunanNil, err := depo.Get(ctx, nilExec.ID)
	require.NoError(t, err)
	assert.Nil(t, okunanNil.Input, "nil girdi NULL olarak dönmeli")

	okunanNull, err := depo.Get(ctx, nullExec.ID)
	require.NoError(t, err)
	assert.Equal(t, json.RawMessage(`null`), okunanNull.Input,
		"JSON null değeri NULL'a çevrilmemeli")

	okunanBos, err := depo.Get(ctx, bosExec.ID)
	require.NoError(t, err)
	assert.Nil(t, okunanBos.Input, "boş dilim NULL olarak yazılmalı")

	// Sütunun gerçekten NULL olduğunu (JSON 'null' metni olmadığını) katalogdan
	// değil, sorgudan doğrula.
	var nilNull, nullNull bool
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT input IS NULL FROM workflow_executions WHERE id = $1`, nilExec.ID).Scan(&nilNull))
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT input IS NULL FROM workflow_executions WHERE id = $1`, nullExec.ID).Scan(&nullNull))
	assert.True(t, nilNull, "nil girdi sütunda NULL olmalı")
	assert.False(t, nullNull, "JSON null sütunda NULL OLMAMALI")
}

// TestAdimJSONNullAyrimi adım çıktısında da aynı ayrımın korunduğunu doğrular.
func TestAdimJSONNullAyrimi(t *testing.T) {
	ctx := context.Background()
	depo := yeniDepo()
	exec := acilanYurutme(ctx, t, depo)

	require.NoError(t, depo.AppendStep(ctx, exec.ID, workflow.StepRecord{
		Name: "nil_cikti", Index: 0, Status: workflow.StepInvoked, Attempts: 1,
	}))
	require.NoError(t, depo.AppendStep(ctx, exec.ID, workflow.StepRecord{
		Name: "null_cikti", Index: 1, Status: workflow.StepInvoked, Attempts: 1,
		Output: json.RawMessage(`null`),
	}))

	okunan, err := depo.Get(ctx, exec.ID)
	require.NoError(t, err)
	require.Len(t, okunan.Steps, 2)
	assert.Nil(t, okunan.Steps[0].Output, "nil adım çıktısı NULL olmalı")
	assert.Equal(t, json.RawMessage(`null`), okunan.Steps[1].Output,
		"JSON null adım çıktısı korunmalı")
}

// TestJSONBOlarakSaklanir alanların metin değil JSONB saklandığını doğrular:
// JSONB sorgulanabilir, metin sorgulanamaz.
func TestJSONBOlarakSaklanir(t *testing.T) {
	ctx := context.Background()
	depo := yeniDepo()

	exec := &workflow.Execution{
		Workflow: wfAdi(t),
		Input:    json.RawMessage(`{"cart_id":"cart_7","satirlar":[{"adet":2}]}`),
	}
	require.NoError(t, depo.Create(ctx, exec))

	var cartID string
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT input->>'cart_id' FROM workflow_executions WHERE id = $1`, exec.ID).Scan(&cartID))
	assert.Equal(t, "cart_7", cartID, "JSONB operatörleri çalışmalı")

	var tur string
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT pg_typeof(input)::text FROM workflow_executions WHERE id = $1`, exec.ID).Scan(&tur))
	assert.Equal(t, "jsonb", tur)
}

// TestJSONBinReddettigiGirdiInvalid JSONB'nin saklayamadığı bir girdinin
// tipli Invalid hatasına çevrildiğini ve kaydın hiç açılmadığını doğrular.
//
// json.Valid bu gövdeyi geçerli sayar; PostgreSQL saymaz. Denetim olmasaydı
// hata sürücüden sınıflandırılmamış (KindInternal) dönerdi: çağıranın verisi
// yüzünden HTTP 500 üretilirdi.
func TestJSONBinReddettigiGirdiInvalid(t *testing.T) {
	ctx := context.Background()
	depo := yeniDepo()
	ad := wfAdi(t)

	// nulEscape ters bölü + u0000'dır; kaynağa doğrudan yazılamaz.
	girdi := json.RawMessage(`{"not":"a` + nulEscape + `b"}`)
	require.True(t, json.Valid(girdi), "gövde json.Valid'i GEÇMELİ; vaka buna dayanıyor")

	err := depo.Create(ctx, &workflow.Execution{Workflow: ad, Input: girdi})

	require.Error(t, err)
	assert.True(t, coreerrors.IsInvalid(err), "çağıran verisi hatası Invalid olmalı: %v", err)
	assert.Equal(t, CodeInvalid, coreerrors.CodeOf(err))

	var adet int
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM workflow_executions WHERE workflow = $1`, ad).Scan(&adet))
	assert.Zero(t, adet, "reddedilen girdiyle kayıt açılmamalı")
}

// TestSQLSTATEKodlariSunucuylaUyusuyor wrapDB'nin eşlediği SQLSTATE kodlarını
// CANLI SUNUCUYA sorarak doğrular.
//
// Kodlar sabit yazılıdır; yanlış yazılsalardı eşleme sessizce genel dala düşer
// ve çağıran verisinden doğan hata KindInternal olarak dönerdi. Burada aynı
// arıza gerçek sunucuda üretilir ve sınıflandırması denetlenir.
func TestSQLSTATEKodlariSunucuylaUyusuyor(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		ad    string
		sorgu string
		deger any
		kod   string
	}{
		{"JSONB'nin çeviremediği kaçış", `SELECT $1::jsonb`, `{"x":"` + nulEscape + `"}`, untranslatableCharacter},
		{"metindeki NUL baytı", `SELECT $1::text`, "a\x00b", notInRepertoire},
	}

	for _, tc := range tests {
		t.Run(tc.ad, func(t *testing.T) {
			_, err := testPool.Pool().Exec(ctx, tc.sorgu, tc.deger)
			require.Error(t, err, "sunucu bu değeri reddetmeli")

			var pgErr *pgconn.PgError
			require.ErrorAs(t, err, &pgErr)
			assert.Equal(t, tc.kod, pgErr.Code, "sabit yazılan SQLSTATE sunucununkiyle aynı olmalı")

			sarmalanmis := wrapDB(err, CodeQueryFailed, "yürütme yazılamadı")
			assert.True(t, coreerrors.IsInvalid(sarmalanmis),
				"çağıran verisinden doğan hata Invalid olmalı: %v", sarmalanmis)

			var tipli *coreerrors.Error
			require.ErrorAs(t, sarmalanmis, &tipli)
			assert.NotContains(t, tipli.Message, pgErr.Message,
				"sürücü mesajı çağıranın verisini taşıyabilir; dışarıya verilen mesaja girmemeli")
		})
	}
}

// TestBozukAriziMetniUcDurumuEngellemez tanı metnindeki yazılamaz baytların
// yürütmeyi "running"de ASILI BIRAKMADIĞINI doğrular.
//
// Metin reddedilseydi uç durum hiç yazılamaz, kayıt sonsuza dek running kalır
// ve o idempotency anahtarı bir daha kullanılamazdı (tekrar her seferinde
// "hâlâ sürüyor" derdi). Bu yüzden açıklama reddedilmez, TEMİZLENİR.
func TestBozukAriziMetniUcDurumuEngellemez(t *testing.T) {
	ctx := context.Background()
	depo := yeniDepo()
	exec := acilanYurutme(ctx, t, depo)

	bozuk := "stok\x00 servisi \xff yanıt vermedi"

	require.NoError(t, depo.AppendStep(ctx, exec.ID, workflow.StepRecord{
		Name: "stok_rezerve", Index: 0, Status: workflow.StepFailed,
		Failure: bozuk, Attempts: 1,
	}), "adım izi tanı metni yüzünden yazılamaz kalmamalı")

	require.NoError(t, depo.UpdateStatus(ctx, exec.ID, workflow.StatusFailed, nil, bozuk),
		"uç durum tanı metni yüzünden yazılamaz kalmamalı")

	okunan, err := depo.Get(ctx, exec.ID)
	require.NoError(t, err)

	assert.Equal(t, workflow.StatusFailed, okunan.Status, "kayıt running'de asılı kalmamalı")
	assert.NotContains(t, okunan.Failure, "\x00", "NUL baytı temizlenmeli")
	assert.Contains(t, okunan.Failure, "stok", "okunabilir kısım korunmalı")
	assert.Contains(t, okunan.Failure, "yanıt vermedi")
	require.Len(t, okunan.Steps, 1)
	assert.NotContains(t, okunan.Steps[0].Failure, "\x00")
	assert.Contains(t, okunan.Steps[0].Failure, "stok")
}

// TestGetCokAdimliYurutmeyiBozmadanOkur yürütme sütunlarının yalnızca İLK
// satırda taranmasının okunan kaydı bozmadığını doğrular.
//
// LEFT JOIN yürütme satırını her adım için tekrar taşır; tarama sınırı kayarsa
// (bkz. skipExecColumns) ya yürütme alanları boş kalır ya da adımlar eksilir.
// Girdi bilinçli olarak büyüktür: tekrar tekrar ayrılan asıl yük odur.
func TestGetCokAdimliYurutmeyiBozmadanOkur(t *testing.T) {
	ctx := context.Background()
	depo := yeniDepo()

	buyukGirdi := json.RawMessage(`{"dolgu":"` + strings.Repeat("g", 64*1024) + `","cart_id":"cart_9"}`)
	exec := &workflow.Execution{
		Workflow:       wfAdi(t),
		IdempotencyKey: "ord_cok_adim",
		Status:         workflow.StatusRunning,
		Input:          buyukGirdi,
	}
	require.NoError(t, depo.Create(ctx, exec))

	const adimSayisi = 6
	for i := range adimSayisi {
		require.NoError(t, depo.AppendStep(ctx, exec.ID, workflow.StepRecord{
			Name:     fmt.Sprintf("adim_%d", i),
			Index:    i,
			Status:   workflow.StepInvoked,
			Output:   json.RawMessage(fmt.Sprintf(`{"sira":%d}`, i)),
			Attempts: i + 1,
		}))
	}

	for ad, oku := range map[string]func() (*workflow.Execution, error){
		"Get": func() (*workflow.Execution, error) {
			return depo.Get(ctx, exec.ID)
		},
		"FindByIdempotencyKey": func() (*workflow.Execution, error) {
			return depo.FindByIdempotencyKey(ctx, exec.Workflow, "ord_cok_adim")
		},
	} {
		t.Run(ad, func(t *testing.T) {
			okunan, err := oku()
			require.NoError(t, err)

			assert.Equal(t, exec.ID, okunan.ID)
			assert.Equal(t, exec.Workflow, okunan.Workflow)
			assert.Equal(t, "ord_cok_adim", okunan.IdempotencyKey)
			assert.Equal(t, workflow.StatusRunning, okunan.Status)
			assert.JSONEq(t, string(buyukGirdi), string(okunan.Input),
				"yürütme girdisi adım sayısından bağımsız olarak eksiksiz dönmeli")
			assert.True(t, okunan.CreatedAt.Equal(exec.CreatedAt))

			require.Len(t, okunan.Steps, adimSayisi)
			for i, adim := range okunan.Steps {
				assert.Equal(t, i, adim.Index)
				assert.Equal(t, fmt.Sprintf("adim_%d", i), adim.Name)
				assert.JSONEq(t, fmt.Sprintf(`{"sira":%d}`, i), string(adim.Output),
					"her adımın kendi çıktısı dönmeli")
				assert.Equal(t, i+1, adim.Attempts)
			}
		})
	}
}

// TestDegerlerParametreOlarakGider SQL enjeksiyonu denemesinin VERİ olarak
// kaldığını doğrular: tırnak ve noktalı virgül içeren değerler aynen saklanır,
// hiçbir ifade çalışmaz.
func TestDegerlerParametreOlarakGider(t *testing.T) {
	ctx := context.Background()
	depo := yeniDepo()

	kotu := `x'; DROP TABLE workflow_execution_steps; --`
	exec := &workflow.Execution{
		Workflow:       wfAdi(t),
		IdempotencyKey: kotu,
		Status:         workflow.StatusRunning,
		Failure:        kotu,
	}
	require.NoError(t, depo.Create(ctx, exec))

	require.NoError(t, depo.AppendStep(ctx, exec.ID, workflow.StepRecord{
		Name: kotu, Index: 0, Status: workflow.StepInvoked, Attempts: 1,
	}))

	okunan, err := depo.FindByIdempotencyKey(ctx, exec.Workflow, kotu)
	require.NoError(t, err)
	assert.Equal(t, kotu, okunan.IdempotencyKey, "değer aynen saklanmalı")
	assert.Equal(t, kotu, okunan.Failure)
	require.Len(t, okunan.Steps, 1)
	assert.Equal(t, kotu, okunan.Steps[0].Name)

	assert.True(t, iliskiVar(ctx, t, testDSN, "workflow_execution_steps"),
		"tablo hâlâ durmalı: değerler ifade olarak yorumlanmamalı")
}

// TestGetIptalEdilmisBaglam iptal edilmiş bağlamın tipli Unavailable hatasına
// çevrildiğini doğrular.
func TestGetIptalEdilmisBaglam(t *testing.T) {
	ctx, iptal := context.WithCancel(context.Background())
	depo := yeniDepo()
	iptal()

	_, err := depo.Get(ctx, "wfx_HERHANGI0000001")

	require.Error(t, err)
	assert.Equal(t, coreerrors.KindUnavailable, coreerrors.KindOf(err),
		"iptal Unavailable olmalı: %v", err)
	assert.Equal(t, CodeCanceled, coreerrors.CodeOf(err))
}

// TestYurutmeSilinceAdimlarDuser ON DELETE CASCADE'in çalıştığını doğrular;
// yürütme kaydı temizlendiğinde adımlar sahipsiz kalmamalıdır.
func TestYurutmeSilinceAdimlarDuser(t *testing.T) {
	ctx := context.Background()
	depo := yeniDepo()
	exec := acilanYurutme(ctx, t, depo)

	require.NoError(t, depo.AppendStep(ctx, exec.ID, workflow.StepRecord{
		Name: "adim", Index: 0, Status: workflow.StepInvoked, Attempts: 1,
	}))

	_, err := testPool.Pool().Exec(ctx, `DELETE FROM workflow_executions WHERE id = $1`, exec.ID)
	require.NoError(t, err)

	var adet int
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM workflow_execution_steps WHERE execution_id = $1`, exec.ID).Scan(&adet))
	assert.Zero(t, adet, "yürütme silinince adımları da düşmeli")
}

// sahteAdim testte kullanılan basit bir workflow adımıdır.
//
// Telafi çağrıldığında adını paylaşılan bir dilime yazar; telafi SIRASI böylece
// ölçülebilir hâle gelir.
type sahteAdim struct {
	ad        string
	cikti     any
	hata      error
	telafiler *[]string
	// yurutmeID doldurulursa adım, çalıştığı yürütmenin kimliğini oraya yazar.
	//
	// Telafi edilmiş bir yürütme idempotency anahtarını BIRAKIR (bkz.
	// [workflow.StatusFailed]), dolayısıyla kaydına anahtardan ulaşılamaz.
	// Kimliği adımdan almak, kaydın hâlâ orada olduğunu kanıtlamanın tek yolu.
	yurutmeID *string
	// calisti doldurulursa adım, Invoke edildiğinde onu true yapar.
	calisti *bool
}

func (a *sahteAdim) Name() string { return a.ad }

func (a *sahteAdim) Invoke(_ context.Context, sc *workflow.StepContext) (any, error) {
	if a.yurutmeID != nil {
		*a.yurutmeID = sc.ExecutionID
	}
	if a.calisti != nil {
		*a.calisti = true
	}
	if a.hata != nil {
		return nil, a.hata
	}
	return a.cikti, nil
}

func (a *sahteAdim) Compensate(_ context.Context, _ *workflow.StepContext) error {
	*a.telafiler = append(*a.telafiler, a.ad)
	return nil
}

// kurtarilabilirAdim durumunu KENDİ kalıcı çıktısından geri kurabilen adımdır.
//
// Telafisi, geri kurulmuş paylaşılan durumu OKUR ve gördüğü değeri kaydeder:
// telafinin gerçekten çalışması yetmez, DOĞRU veriyle çalıştığı da görülmelidir.
// Kaydın çıktısından gelmeyen bir değerle çalışan telafi, geri almadığı işi
// geri aldım der.
type kurtarilabilirAdim struct {
	ad        string
	cikti     any
	telafiler *[]string
	// gorulen, Compensate'in paylaşılan haritada bulduğu değerdir.
	gorulen *string
	// restoreHatasi doluysa Restore o hatayla düşer.
	restoreHatasi error
}

func (a *kurtarilabilirAdim) Name() string { return a.ad }

func (a *kurtarilabilirAdim) Invoke(_ context.Context, sc *workflow.StepContext) (any, error) {
	sc.Shared[a.ad] = a.cikti

	return a.cikti, nil
}

func (a *kurtarilabilirAdim) Compensate(_ context.Context, sc *workflow.StepContext) error {
	*a.telafiler = append(*a.telafiler, a.ad)
	if a.gorulen != nil {
		deger, _ := sc.Shared[a.ad].(string)
		*a.gorulen = deger
	}

	return nil
}

func (a *kurtarilabilirAdim) Restore(sc *workflow.StepContext, output json.RawMessage) error {
	if a.restoreHatasi != nil {
		return a.restoreHatasi
	}

	var deger string
	if err := json.Unmarshal(output, &deger); err != nil {
		return err
	}
	sc.Shared[a.ad] = deger

	return nil
}

// engelleyiciAdim kaydı yokken çalışmamış SAYILAMAYAN adımdır (tahsilat gibi).
type engelleyiciAdim struct {
	kurtarilabilirAdim
}

func (a *engelleyiciAdim) BlocksRecovery() {}

// TestMotorlaBasariliKosuKaliciOlur gerçek motorun bu depoyla çalıştığını ve
// başarılı koşunun completed olarak kalıcılaştığını doğrular (Faz 3 DoD).
//
// Motor ile depo AYRI paketlerdir ve birbirini import etmez; sözleşmenin
// gerçekten örtüştüğü ancak ikisi birlikte koşturulunca görülür.
func TestMotorlaBasariliKosuKaliciOlur(t *testing.T) {
	ctx := context.Background()
	depo := yeniDepo()
	motor := workflow.New(depo, nil)

	var telafiler []string
	wf := workflow.Workflow{
		Name: wfAdi(t),
		Steps: []workflow.Step{
			&sahteAdim{ad: "stok_rezerve", cikti: map[string]any{"rezervasyon": "rez_1"}, telafiler: &telafiler},
			&sahteAdim{ad: "odeme_al", cikti: map[string]any{"odeme": "pay_1"}, telafiler: &telafiler},
			&sahteAdim{ad: "siparis_olustur", cikti: map[string]any{"order_id": "ord_9"}, telafiler: &telafiler},
		},
	}

	cikti, err := motor.Run(ctx, wf, map[string]any{"cart_id": "cart_1"},
		workflow.WithIdempotencyKey("ord_e2e"))

	require.NoError(t, err)
	ham, jsonMu := cikti.(json.RawMessage)
	require.Truef(t, jsonMu, "Run çıktıyı json.RawMessage olarak döner, dönen tip: %T", cikti)
	assert.JSONEq(t, `{"order_id":"ord_9"}`, string(ham))
	assert.Empty(t, telafiler, "başarılı koşuda hiçbir telafi çalışmaz")

	kalici, err := depo.FindByIdempotencyKey(ctx, wf.Name, "ord_e2e")
	require.NoError(t, err)

	assert.Equal(t, workflow.StatusCompleted, kalici.Status, "başarılı koşu completed olmalı")
	assert.JSONEq(t, `{"cart_id":"cart_1"}`, string(kalici.Input))
	assert.JSONEq(t, `{"order_id":"ord_9"}`, string(kalici.Output))
	assert.Empty(t, kalici.Failure)

	require.Len(t, kalici.Steps, 3, "üç adımın izi de kalmalı")
	for i, adim := range kalici.Steps {
		assert.Equal(t, i, adim.Index, "adımlar Index sırasında dönmeli")
		assert.Equal(t, workflow.StepInvoked, adim.Status)
		assert.GreaterOrEqual(t, adim.Attempts, 1, "deneme sayısı en az 1 olmalı")
		assert.False(t, adim.StartedAt.IsZero(), "başlangıç zamanı yazılmalı")
		assert.False(t, adim.EndedAt.IsZero(), "bitiş zamanı yazılmalı")
	}
	assert.Equal(t, "siparis_olustur", kalici.Steps[2].Name)
}

// TestMotorlaTelafiKaliciOlur bir adım patladığında telafinin TERS SIRADA
// çalıştığını ve izinin doğru kalıcılaştığını doğrular.
//
// Burada AppendStep'in güncelleme yolu gerçek kullanımıyla sınanır: 0. ve 1.
// adımlar önce invoked, sonra compensated olarak yazılır; kayıt yine üç
// satırdır, altı değil.
func TestMotorlaTelafiKaliciOlur(t *testing.T) {
	var yurutmeID string
	ctx := context.Background()
	depo := yeniDepo()
	motor := workflow.New(depo, nil)

	var telafiler []string
	wf := workflow.Workflow{
		Name: wfAdi(t),
		Steps: []workflow.Step{
			&sahteAdim{ad: "stok_rezerve", cikti: "rez_1", telafiler: &telafiler},
			&sahteAdim{ad: "odeme_al", cikti: "pay_1", telafiler: &telafiler},
			&sahteAdim{ad: "siparis_olustur", hata: coreerrors.Internal("patladi", "sipariş yazılamadı"), telafiler: &telafiler, yurutmeID: &yurutmeID},
		},
	}

	_, err := motor.Run(ctx, wf, map[string]any{"cart_id": "cart_2"},
		workflow.WithIdempotencyKey("ord_e2e_telafi"))

	require.Error(t, err)
	assert.Equal(t, []string{"odeme_al", "stok_rezerve"}, telafiler,
		"telafi TERS sırada çalışmalı")

	// Telafi tamamlandıysa yürütme dünyada iz bırakmamıştır ve anahtarı da
	// bırakılmıştır: aynı anahtarla gelen bir sonraki çağrı 409 değil YENİ bir
	// yürütme almalıdır (bkz. [workflow.StatusFailed]).
	_, anahtarErr := depo.FindByIdempotencyKey(ctx, wf.Name, "ord_e2e_telafi")
	require.Error(t, anahtarErr, "telafi edilen yürütme anahtarı TUTMAMALI")
	assert.True(t, coreerrors.IsNotFound(anahtarErr))

	// Kayıt SİLİNMEZ; yalnızca anahtarı düşer. Kimliğe adımdan ulaşılır.
	require.NotEmpty(t, yurutmeID, "adım yürütme kimliğini yazmalı")
	kalici, err := depo.Get(ctx, yurutmeID)
	require.NoError(t, err, "başarısız deneme denetim kaydı olarak KALMALI")

	assert.Equal(t, workflow.StatusFailed, kalici.Status, "telafi tamamlandıysa durum failed olmalı")
	assert.NotEmpty(t, kalici.Failure, "arıza açıklaması kalıcılaşmalı")

	require.Len(t, kalici.Steps, 3, "her adım tek satır olmalı; güncelleme yeni satır açmaz")
	assert.Equal(t, workflow.StepCompensated, kalici.Steps[0].Status)
	assert.Equal(t, workflow.StepCompensated, kalici.Steps[1].Status)
	assert.Equal(t, workflow.StepFailed, kalici.Steps[2].Status,
		"patlayan adım telafi edilmez, failed kalır")
	assert.Contains(t, kalici.Steps[2].Failure, "sipariş yazılamadı")
}

// iliskiVar verilen adda bir ilişkinin (tablo ya da indeks) var olup
// olmadığını bildirir.
func iliskiVar(ctx context.Context, t *testing.T, dsn, ad string) bool {
	t.Helper()

	conn, err := pgx.Connect(ctx, dsn)
	require.NoError(t, err)
	defer func() { _ = conn.Close(ctx) }()

	var varMi bool
	require.NoError(t, conn.QueryRow(ctx,
		`SELECT EXISTS (
			SELECT 1 FROM pg_class
			WHERE relname = $1 AND relnamespace = current_schema()::regnamespace
		)`, ad).Scan(&varMi))
	return varMi
}

// yeniVeritabani teste özel boş bir veritabanı açar ve adresini döner.
// Veritabanı test bitiminde düşürülür.
func yeniVeritabani(ctx context.Context, t *testing.T) string {
	t.Helper()

	ad := fmt.Sprintf("gobit_wf_%d", time.Now().UnixNano())

	conn, err := pgx.Connect(ctx, yonetimDSN)
	require.NoError(t, err)
	defer func() { _ = conn.Close(ctx) }()

	// Veritabanı adı parametrelenemez; ad testin ürettiği sabit biçimdedir
	// (yalnızca harf, alt çizgi ve rakam), dışarıdan veri almaz.
	_, err = conn.Exec(ctx, `CREATE DATABASE `+ad)
	require.NoError(t, err)

	t.Cleanup(func() {
		temizlik := context.Background()
		c, cErr := pgx.Connect(temizlik, yonetimDSN)
		if cErr != nil {
			return
		}
		defer func() { _ = c.Close(temizlik) }()
		_, _ = c.Exec(temizlik, `DROP DATABASE IF EXISTS `+ad+` WITH (FORCE)`)
	})

	u, err := url.Parse(yonetimDSN)
	require.NoError(t, err)
	u.Path = "/" + ad
	return u.String()
}

// terkEdilmisYurutmeKur iş yapmış ama bayat bir yürütme kurar ve kimliğini döner.
//
// Zamanı geri almak ŞART: AppendStep yürütmenin updated_at'ini tazeler, yani
// "adımı var VE bayat" durumu depo yüzeyinden kurulamaz. Üretim aynı duruma
// çökerek varır.
func terkEdilmisYurutmeKur(
	ctx context.Context, t *testing.T, depo workflow.Store, wf workflow.Workflow,
	anahtar, id string, kayitlar []workflow.StepRecord,
) {
	t.Helper()

	exec := &workflow.Execution{ID: id, Workflow: wf.Name, IdempotencyKey: anahtar, Status: workflow.StatusRunning}
	require.NoError(t, depo.Create(ctx, exec))
	for _, kayit := range kayitlar {
		require.NoError(t, depo.AppendStep(ctx, id, kayit))
	}

	_, err := testPool.Pool().Exec(ctx,
		`UPDATE workflow_executions SET updated_at = now() - interval '1 hour' WHERE id = $1`, id)
	require.NoError(t, err)
}

// TestTerkEdilmisYurutmeKayitlardanTelafiEdilir kurtarmanın kendisini kanıtlar.
//
// Süreç iş yaptıktan sonra ölmüşse ayrılmış stok dünyada durur ve onu bırakacak
// tek şey telafi zinciridir. Motor telafi işlevlerine sahiptir; kaybolan tek şey
// adımlar arası paylaşılan durumdu ve o, adımın KENDİ kalıcı çıktısından geri
// kurulur (workflow.Recoverable).
//
// İki iddia birden sınanır ve ikincisi asıl olandır: telafi ÇALIŞTI, ve DOĞRU
// veriyle çalıştı. Yalnızca "çalıştı" diyen bir test, Shared'ı boş bırakan bir
// kurtarmayı da geçirirdi — o telafi de "bir şey bırakmadım" derdi.
func TestTerkEdilmisYurutmeKayitlardanTelafiEdilir(t *testing.T) {
	ctx := context.Background()
	depo := yeniDepo()
	motor := workflow.New(depo, nil)

	telafiler := []string{}
	var gorulen string
	adim := &kurtarilabilirAdim{ad: "stok_rezerve", cikti: "res_1", telafiler: &telafiler, gorulen: &gorulen}
	wf := workflow.Workflow{Name: "TestTerkEdilmisYurutmeKayitlardanTelafiEdilir", Steps: []workflow.Step{adim}}

	const anahtar = "terk_kurtarilir"
	const id = "wfx_TERK_KURTAR"
	terkEdilmisYurutmeKur(ctx, t, depo, wf, anahtar, id, []workflow.StepRecord{{
		Name: "stok_rezerve", Index: 0, Status: workflow.StepInvoked, Attempts: 1,
		Output: []byte(`"res_1"`),
	}})

	_, err := motor.Run(ctx, wf, nil,
		workflow.WithIdempotencyKey(anahtar), workflow.WithLease(time.Minute))
	require.NoError(t, err, "kurtarılabilir bir yürütme yeniden denemeyi ENGELLEMEMELİ")

	assert.Equal(t, []string{"stok_rezerve"}, telafiler,
		"terk edilmiş yürütmenin telafisi kayıtlardan çalıştırılmalı")
	assert.Equal(t, "res_1", gorulen,
		"telafi, kaydın çıktısından geri kurulan değeri görmeli; boş Shared ile çalışan "+
			"bir telafi bırakacağı rezervasyonu bulamaz")

	kalici, err := depo.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, workflow.StatusFailed, kalici.Status,
		"telafi eksiksiz tamamlandıysa durum failed'dır ve anahtar bırakılır")
	assert.Empty(t, kalici.IdempotencyKey,
		"anahtar bırakılmalı; müşteri aynı sepeti yeniden ödeyebilmeli")
}

// TestKurtarmaKaydiOlmayanEngelleyiciAdimdaDURUR kurtarmanın sınırını çizer ve
// bu sınır ödeme yüzünden vardır.
//
// Motor adımın kaydını Invoke DÖNDÜKTEN SONRA yazar, dolayısıyla Invoke'un
// ortasında ölen süreç o adımdan hiçbir iz bırakmaz. Kurtarma kayıtlara bakar,
// yani böyle bir adımı "hiç çalışmamış" sayar. Tahsilat için bu, kartı çekilmiş
// bir müşterinin stoğunun bırakılıp anahtarının serbest kalması ve İKİNCİ KEZ
// tahsil edilmesi demektir. Adım bunu workflow.RecoveryBlocker ile bildirir ve
// karar elle müdahaleye döner.
func TestKurtarmaKaydiOlmayanEngelleyiciAdimdaDURUR(t *testing.T) {
	ctx := context.Background()
	depo := yeniDepo()
	motor := workflow.New(depo, nil)

	telafiler := []string{}
	rezerve := &kurtarilabilirAdim{ad: "stok_rezerve", cikti: "res_1", telafiler: &telafiler}
	tahsilat := &engelleyiciAdim{kurtarilabilirAdim: kurtarilabilirAdim{
		ad: "tahsilat", cikti: "pay_1", telafiler: &telafiler}}
	wf := workflow.Workflow{
		Name:  "TestKurtarmaKaydiOlmayanEngelleyiciAdimdaDURUR",
		Steps: []workflow.Step{rezerve, tahsilat},
	}

	const anahtar = "terk_engelleyici"
	const id = "wfx_TERK_ENGEL"
	// YALNIZCA ilk adımın kaydı var: süreç tahsilatın içinde ölmüş OLABİLİR.
	terkEdilmisYurutmeKur(ctx, t, depo, wf, anahtar, id, []workflow.StepRecord{{
		Name: "stok_rezerve", Index: 0, Status: workflow.StepInvoked, Attempts: 1,
		Output: []byte(`"res_1"`),
	}})

	_, err := motor.Run(ctx, wf, nil,
		workflow.WithIdempotencyKey(anahtar), workflow.WithLease(time.Minute))

	require.Error(t, err)
	assert.True(t, coreerrors.IsConflict(err), "hata: %v", err)
	assert.Contains(t, err.Error(), "A HUMAN IS NEEDED")
	assert.Empty(t, telafiler,
		"tahsilat uçuşta olmuş olabilir; hiçbir telafi çalıştırılmamalı")

	kalici, err := depo.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, workflow.StatusCompensationFailed, kalici.Status)
	assert.Equal(t, anahtar, kalici.IdempotencyKey,
		"anahtar TUTULMALI; bırakmak müşterinin yeniden ödemesine ve ikinci kez "+
			"tahsil edilmesine kapı açardı")
}

// TestKurtarmaTanimDegismisseYapilmaz iki dağıtım arasında değişen bir workflow
// tanımına karşı korur.
//
// İndeks kaydın kimliğidir ama tanım değişmiş olabilir; ad denetimi olmasaydı
// 2. adımın telafisi bambaşka bir adımın çıktısıyla çağrılırdı.
func TestKurtarmaTanimDegismisseYapilmaz(t *testing.T) {
	ctx := context.Background()
	depo := yeniDepo()
	motor := workflow.New(depo, nil)

	telafiler := []string{}
	adim := &kurtarilabilirAdim{ad: "YENI_AD", cikti: "res_1", telafiler: &telafiler}
	wf := workflow.Workflow{Name: "TestKurtarmaTanimDegismisseYapilmaz", Steps: []workflow.Step{adim}}

	const anahtar = "terk_tanim_degisti"
	const id = "wfx_TERK_TANIM"
	terkEdilmisYurutmeKur(ctx, t, depo, wf, anahtar, id, []workflow.StepRecord{{
		Name: "ESKI_AD", Index: 0, Status: workflow.StepInvoked, Attempts: 1,
		Output: []byte(`"res_1"`),
	}})

	_, err := motor.Run(ctx, wf, nil,
		workflow.WithIdempotencyKey(anahtar), workflow.WithLease(time.Minute))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "A HUMAN IS NEEDED")
	assert.Empty(t, telafiler, "adı tutmayan bir tanımla hiçbir telafi çalıştırılmamalı")
}

// TestKurtarmaRestorePatlarsaYapilmaz eksik durumla koşan telafiyi engeller.
//
// Restore, kayıttaki çıktıyı Shared'a geri koyamıyorsa (çıktı boş ya da şekli
// değişmiş) telafi neyi geri alacağını bilemez. Sessizce boş durumla koşmak,
// "başardım" diyen ama hiçbir şey bırakmayan bir telafi üretirdi.
func TestKurtarmaRestorePatlarsaYapilmaz(t *testing.T) {
	ctx := context.Background()
	depo := yeniDepo()
	motor := workflow.New(depo, nil)

	telafiler := []string{}
	adim := &kurtarilabilirAdim{
		ad: "stok_rezerve", cikti: "res_1", telafiler: &telafiler,
		restoreHatasi: coreerrors.Internal("cikti_bozuk", "çıktı çözülemedi"),
	}
	wf := workflow.Workflow{Name: "TestKurtarmaRestorePatlarsaYapilmaz", Steps: []workflow.Step{adim}}

	const anahtar = "terk_restore_patlar"
	const id = "wfx_TERK_RESTORE"
	terkEdilmisYurutmeKur(ctx, t, depo, wf, anahtar, id, []workflow.StepRecord{{
		Name: "stok_rezerve", Index: 0, Status: workflow.StepInvoked, Attempts: 1,
		Output: []byte(`"res_1"`),
	}})

	_, err := motor.Run(ctx, wf, nil,
		workflow.WithIdempotencyKey(anahtar), workflow.WithLease(time.Minute))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "A HUMAN IS NEEDED")
	assert.Empty(t, telafiler, "durumu geri kurulamayan bir zincir telafi edilmemeli")
}

// TestTerkEdilmisYurutmeIsYapmissaElleMudahaleIster çökmenin TEHLİKELİ yarısını
// gerçek depoda kanıtlar.
//
// # Neden burada ve bellek deposunda değil
//
// Kurulacak durum "adımı var VE bayat"tır ve iki deponun ORTAK davranışı onu
// depo yüzeyinden kurulamaz kılar: AppendStep yürütmenin updated_at'ini TAZELER.
// Bu doğrudur — ilerleyen bir saga kirasını canlı tutmalıdır — ama testin zamanı
// geri alması gerektiği anlamına gelir, ki bunu ancak gerçek satırı güncelleyerek
// yapabiliriz. Üretim aynı duruma ÇÖKEREK varır: süreç adımı yazdıktan sonra
// ölür ve updated_at olduğu yerde kalır.
//
// # İddia
//
// Süreç iş yaptıktan sonra kesilmişse telafi HİÇ çalışmamıştır: ayrılmış stok ve
// açılmış ödeme oturumu ortada durur. Sessizce yeniden denemek o stoğu İKİNCİ
// KEZ ayırmak olurdu. Kayıt bu yüzden compensation_failed'a taşınır, anahtarını
// TUTAR ve çağıran elle müdahale gerektiğini söyleyen bir çakışma alır.
func TestTerkEdilmisYurutmeIsYapmissaElleMudahaleIster(t *testing.T) {
	ctx := context.Background()
	depo := yeniDepo()
	motor := workflow.New(depo, nil)

	var kosuldu bool
	wf := workflow.Workflow{
		Name: "TestTerkEdilmisYurutmeIsYapmissaElleMudahaleIster",
		Steps: []workflow.Step{&sahteAdim{ad: "stok_rezerve", cikti: "res_1", telafiler: &[]string{},
			calisti: &kosuldu}},
	}

	const anahtar = "terk_is_yapilmis"
	exec := &workflow.Execution{
		ID: "wfx_TERK_PG", Workflow: wf.Name, IdempotencyKey: anahtar,
		Status: workflow.StatusRunning,
	}
	require.NoError(t, depo.Create(ctx, exec))
	require.NoError(t, depo.AppendStep(ctx, exec.ID, workflow.StepRecord{
		Name: "stok_rezerve", Index: 0, Status: workflow.StepInvoked, Attempts: 1,
		Output: []byte(`"res_1"`),
	}))

	// Zamanı geri al: süreç adımı yazdıktan sonra ölmüş ve bir saat geçmiş.
	_, err := testPool.Pool().Exec(ctx,
		`UPDATE workflow_executions SET updated_at = now() - interval '1 hour' WHERE id = $1`, exec.ID)
	require.NoError(t, err)

	_, err = motor.Run(ctx, wf, nil,
		workflow.WithIdempotencyKey(anahtar), workflow.WithLease(time.Minute))

	require.Error(t, err)
	assert.True(t, coreerrors.IsConflict(err), "hata: %v", err)
	assert.Contains(t, err.Error(), "A HUMAN IS NEEDED")
	assert.False(t, kosuldu, "yarım işin üstüne HİÇBİR adım çalıştırılmamalı")

	kalici, err := depo.Get(ctx, exec.ID)
	require.NoError(t, err)
	assert.Equal(t, workflow.StatusCompensationFailed, kalici.Status,
		"durum olan biteni SÖYLEMELİ: iş yapıldı, telafi çalışmadı")
	assert.Equal(t, anahtar, kalici.IdempotencyKey,
		"anahtar TUTULMALI; bırakılsaydı yarım işin üstüne yeni bir deneme binerdi")
}
