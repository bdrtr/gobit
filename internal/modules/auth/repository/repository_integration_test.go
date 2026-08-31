//go:build integration

// Bu dosyadaki testler gerçek bir PostgreSQL örneği (dolayısıyla Docker)
// gerektirir; `make test` hızlı kalsın diye `integration` etiketiyle
// ayrılmıştır. Çalıştırmak için: make test-integration
//
// Paket içi birim testi ([TestWrapDBSQLSTATESiniflariniAyirir]) hata
// sınıflandırmasının KARARINI kanıtlar. Buradaki testler kararın dayandığı
// ZEMİNİ kanıtlar: şemadaki benzersizliklerin gerçekten uygulandığını,
// yumuşak silinmiş bir kanala bağ kurulamadığını, anahtar + bağ yazımının tek
// işlem olduğunu ve PostgreSQL'in gerçekten beklenen SQLSTATE'i ürettiğini.
// Hiçbiri sahte bir sürücüyle sınanamaz.
package repository_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/internal/core/db"
	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/auth"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
	"github.com/bdrtr/gobit/internal/modules/auth/repository"
	"github.com/bdrtr/gobit/internal/modules/auth/service"
)

const postgresImage = "postgres:16-alpine"

// testPool tüm testlerin paylaştığı havuzdur.
var testPool *db.Pool

func TestMain(m *testing.M) {
	os.Exit(runWithPostgres(m))
}

// runWithPostgres tek bir Postgres konteyneri kaldırıp tüm testleri onun
// üzerinde çalıştırır. os.Exit defer'ları atladığı için ayrı fonksiyondadır.
func runWithPostgres(m *testing.M) int {
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx, postgresImage,
		tcpostgres.WithDatabase("gobit_auth"),
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

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "bağlantı adresi alınamadı: %v\n", err)
		return 1
	}

	testPool, err = db.New(ctx, db.DefaultConfig(dsn), nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bağlantı havuzu açılamadı: %v\n", err)
		return 1
	}
	defer testPool.Close()

	// Migration kaynağı modülün kendi embed.FS'idir: testin uyguladığı şema,
	// sunucunun açılışta uyguladığının ta kendisidir.
	if err := db.Migrate(ctx, dsn, auth.New(auth.Options{}).Migrations(), auth.ModuleName); err != nil {
		fmt.Fprintf(os.Stderr, "migration uygulanamadı: %v\n", err)
		return 1
	}

	return m.Run()
}

// yeniDepo gerçek havuz üzerinde çalışan bir depo üretir.
func yeniDepo(t *testing.T) *repository.Repo {
	t.Helper()

	return repository.New(testPool.Pool())
}

// yeniKullanici test için benzersiz e-postalı bir yönetim kullanıcısı yazar.
func yeniKullanici(ctx context.Context, t *testing.T, repo *repository.Repo) models.User {
	t.Helper()

	now := time.Now().UTC()
	id := models.NewUserID(now)
	// E-posta KÜÇÜK harf olmalıdır (auth_user_email_check); kimlik gövdesi
	// Crockford Base32 olduğu için büyük harf taşır.
	user, err := repo.CreateUser(ctx, models.User{
		ID:        id,
		Email:     strings.ToLower("u" + id[len(models.UserIDPrefix):] + "@ornek.test"),
		Scopes:    []string{models.ScopeAdmin},
		CreatedAt: now,
	}, nil)
	require.NoError(t, err)

	return user
}

// yeniKanal test için benzersiz adlı bir satış kanalı yazar.
func yeniKanal(ctx context.Context, t *testing.T, repo *repository.Repo) models.SalesChannel {
	t.Helper()

	now := time.Now().UTC()
	id := models.NewSalesChannelID(now)
	channel, err := repo.CreateSalesChannel(ctx, models.SalesChannel{
		ID:        id,
		Name:      "kanal " + id,
		CreatedAt: now,
	})
	require.NoError(t, err)

	return channel
}

// anahtarKaydi yazılmaya hazır bir publishable anahtar kaydı üretir.
//
// Düz metin döndürülmez: bu testlerin hiçbiri anahtarın kendisiyle ilgilenmez,
// yalnızca satırın yazılıp yazılmadığıyla ilgilenir.
func anahtarKaydi(t *testing.T) models.APIKey {
	t.Helper()

	plaintext, err := models.NewToken(models.APIKeyPublishable)
	require.NoError(t, err)

	now := time.Now().UTC()
	return models.APIKey{
		ID:        models.NewAPIKeyID(now),
		Type:      models.APIKeyPublishable,
		Title:     "test anahtarı " + now.Format(time.RFC3339Nano),
		TokenHash: models.HashToken(plaintext),
		Redacted:  models.RedactToken(plaintext),
		Scopes:    []string{},
		CreatedAt: now,
	}
}

// sayim tek sütunluk bir sayım sorgusunu çalıştırır.
func sayim(ctx context.Context, t *testing.T, sql string, args ...any) int64 {
	t.Helper()

	var n int64
	require.NoError(t, testPool.Pool().QueryRow(ctx, sql, args...).Scan(&n))

	return n
}

// TestKullaniciBasinaSaglayiciBasinaTekKimlikVardir
// auth_identity_user_provider_uniq indeksinin gerçekten uygulandığını
// kanıtlar.
//
// Kural neden şart: kimlik (user_id, provider) ile TEK satır olarak okunur ve
// parola, deneme sayacı ile kilit hep o satıra yazılır. İkinci bir satır
// açılabilseydi hangisinin okunacağı belirsizleşir, kilit sayacı ikiye
// bölünürdü. İkinci satır depo yüzeyinden açılamadığı için ham SQL ile
// denenir: sınanan şey KODUN değil, ŞEMANIN verdiği garantidir.
func TestKullaniciBasinaSaglayiciBasinaTekKimlikVardir(t *testing.T) {
	ctx := context.Background()
	repo := yeniDepo(t)
	user := yeniKullanici(ctx, t, repo)
	now := time.Now().UTC()

	_, err := repo.SetPasswordHash(ctx, user.ID, models.ProviderEmailPass, user.Email, "hash-1", now)
	require.NoError(t, err)

	_, err = testPool.Pool().Exec(ctx,
		`INSERT INTO auth_identity (id, user_id, provider, provider_identity)
		 VALUES ($1, $2, $3, $4)`,
		models.NewAuthIdentityID(now), user.ID, models.ProviderEmailPass, "ikinci-"+user.Email)
	require.Error(t, err, "aynı kullanıcı ve sağlayıcı için ikinci kimlik yazılabildi")

	var pgErr *pgconn.PgError
	require.True(t, errors.As(err, &pgErr))
	assert.Equal(t, "23505", pgErr.Code)
	assert.Equal(t, "auth_identity_user_provider_uniq", pgErr.ConstraintName)
}

// TestParolaAtamaIkinciKimlikAcmaz [repository.Repo.SetPasswordHash]'in var
// olan kimliği GÜNCELLEDİĞİNİ, yeni satır açmadığını kanıtlar.
//
// Benzersizlik kısıtının pratikteki karşılığı budur: "parola ata" çağrısı
// tekrarlandığında kimlik sayısı sabit kalmalıdır.
func TestParolaAtamaIkinciKimlikAcmaz(t *testing.T) {
	ctx := context.Background()
	repo := yeniDepo(t)
	user := yeniKullanici(ctx, t, repo)
	now := time.Now().UTC()

	ilk, err := repo.SetPasswordHash(ctx, user.ID, models.ProviderEmailPass, user.Email, "hash-1", now)
	require.NoError(t, err)

	ikinci, err := repo.SetPasswordHash(ctx, user.ID, models.ProviderEmailPass, user.Email, "hash-2", now.Add(time.Second))
	require.NoError(t, err)

	assert.Equal(t, ilk.ID, ikinci.ID, "ikinci çağrı yeni kimlik açmamalı")
	assert.Equal(t, "hash-2", ikinci.PasswordHash)
	assert.Equal(t, int64(1), sayim(ctx,
		t, `SELECT count(*) FROM auth_identity WHERE user_id = $1 AND deleted_at IS NULL`, user.ID))
}

// TestCikisCapayiIlerletirKimlikBilgisineDokunmaz
// [repository.Repo.RevokeSessions] sorgusunun SÖZLEŞMESİNİ gerçek veritabanında
// kanıtlar.
//
// İkisi de aynı ölçüde şarttır:
//
//   - updated_at İLERLEMELİDİR — oturum iptalinin dayandığı çapa odur, yerinde
//     kalsaydı çıkış ucu 200 döner ve hiçbir jetonu düşürmezdi.
//   - password_hash ile kilit sayaçları YERİNDE KALMALIDIR — parola değişseydi
//     kullanıcı bir daha giremezdi, sayaç sıfırlansaydı çıkış ucu giriş
//     kilidini temizlemenin yolu olurdu.
//
// Sözleşme yalnızca gerçek SQL ile sınanabilir: sahte bir depo, sorgunun SET
// listesinde ne yazdığını göremez.
func TestCikisCapayiIlerletirKimlikBilgisineDokunmaz(t *testing.T) {
	ctx := context.Background()
	repo := yeniDepo(t)
	user := yeniKullanici(ctx, t, repo)
	now := time.Now().UTC()

	onceki, err := repo.SetPasswordHash(ctx, user.ID, models.ProviderEmailPass, user.Email, "hash-1", now)
	require.NoError(t, err)

	// Kilit çıkıştan ÖNCE kurulur; korunup korunmadığı ancak gerçekten kilitli
	// bir kayıtta görülebilir. Eşik 1'dir: tek başarısız deneme kilitler.
	kilitli, err := repo.RegisterLoginFailure(ctx, onceki.ID, 1, now.Add(time.Minute), now)
	require.NoError(t, err)
	require.Equal(t, 1, kilitli.FailedAttempts)
	require.True(t, kilitli.IsLocked(now), "test zemini: kayıt çıkıştan önce kilitli olmalı")

	cikis := now.Add(2 * time.Second)
	sonraki, err := repo.RevokeSessions(ctx, user.ID, models.ProviderEmailPass, cikis)
	require.NoError(t, err)

	assert.Equal(t, onceki.ID, sonraki.ID, "çıkış yeni kimlik satırı açmamalı")
	assert.True(t, sonraki.UpdatedAt.After(onceki.UpdatedAt),
		"oturum çapası ilerlemeli: önce %s, sonra %s", onceki.UpdatedAt, sonraki.UpdatedAt)
	assert.Equal(t, "hash-1", sonraki.PasswordHash, "çıkış parolayı değiştirmemeli")
	assert.Equal(t, 1, sonraki.FailedAttempts, "çıkış başarısız deneme sayacını sıfırlamamalı")
	assert.True(t, sonraki.IsLocked(cikis),
		"çıkış giriş kilidini kaldırmamalı; kaldırsaydı kilidi atlatmanın yolu olurdu")
}

// TestKimliksizKullaniciCikisYapamaz giriş kimliği hiç olmayan bir kullanıcıda
// çıkışın SESSİZCE başarılı olmadığını kanıtlar.
//
// Yazılacak satır yoksa yazılan çapa da yoktur; başarılı dönmek, hiçbir şey
// düşürmeyen bir çıkışı başarı gibi göstermek olurdu.
func TestKimliksizKullaniciCikisYapamaz(t *testing.T) {
	ctx := context.Background()
	repo := yeniDepo(t)
	user := yeniKullanici(ctx, t, repo)

	_, err := repo.RevokeSessions(ctx, user.ID, models.ProviderEmailPass, time.Now().UTC())

	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "beklenen tür NotFound, gelen: %s", errors.KindOf(err))
	assert.Equal(t, repository.CodeIdentityNotFound, errors.CodeOf(err))
}

// TestSilinmisKanalaAnahtarBaglanamaz yumuşak silinmiş bir kanala bağ
// kurulmadığını kanıtlar.
//
// Foreign key bu durumu yakalamaz: silinen satır yerinde durur ve FK'yi geçer.
// Bağ kurulabilseydi anahtar ÖLÜ DOĞARDI — yönetim yüzeyinde "kanala bağlı"
// görünür, mağaza isteğinde hiçbir kanal bulamaz ve reddedilirdi.
func TestSilinmisKanalaAnahtarBaglanamaz(t *testing.T) {
	ctx := context.Background()
	repo := yeniDepo(t)
	channel := yeniKanal(ctx, t, repo)
	now := time.Now().UTC()

	key, err := repo.CreateAPIKey(ctx, anahtarKaydi(t))
	require.NoError(t, err)
	require.NoError(t, repo.DeleteSalesChannel(ctx, channel.ID, now))

	err = repo.LinkSalesChannel(ctx, key.ID, channel.ID, now)
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "silinmiş kanal 'bulunamadı' olmalı, tür: %s", errors.KindOf(err))
	assert.Equal(t, repository.CodeSalesChannelNotFound, errors.CodeOf(err))
	assert.Equal(t, int64(0), sayim(ctx,
		t, `SELECT count(*) FROM api_key_sales_channel WHERE api_key_id = $1`, key.ID))
}

// TestCanliKanalaBaglanabilir denetimin doğru kapıyı kapattığını, kapıyı
// tümden kapatmadığını kanıtlar.
//
// Devre dışı (is_disabled) kanal da bağlanabilir: devre dışı olmak silinmiş
// olmak değildir ve yönetim yüzeyi bağı önceden kurup kanalı sonra açabilmeli.
func TestCanliKanalaBaglanabilir(t *testing.T) {
	ctx := context.Background()
	repo := yeniDepo(t)
	channel := yeniKanal(ctx, t, repo)
	now := time.Now().UTC()

	key, err := repo.CreateAPIKey(ctx, anahtarKaydi(t))
	require.NoError(t, err)

	require.NoError(t, repo.LinkSalesChannel(ctx, key.ID, channel.ID, now))
	// Aynı bağın tekrarı hata değildir: bağ kümedir, çokluk taşımaz.
	require.NoError(t, repo.LinkSalesChannel(ctx, key.ID, channel.ID, now))

	ids, err := repo.ChannelIDsOfKey(ctx, key.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{channel.ID}, ids)

	disabled := true
	_, err = repo.UpdateSalesChannel(ctx, channel.ID, models.SalesChannelPatch{IsDisabled: &disabled}, now)
	require.NoError(t, err)

	ikinciAnahtar, err := repo.CreateAPIKey(ctx, anahtarKaydi(t))
	require.NoError(t, err)
	assert.NoError(t, repo.LinkSalesChannel(ctx, ikinciAnahtar.ID, channel.ID, now),
		"devre dışı kanal silinmiş sayılmamalı")
}

// TestAnahtarVeBaglariTekIslemdeYazilir bağ kurulamadığında anahtar satırının
// da KALMADIĞINI kanıtlar.
//
// Yazım iki işleme bölünseydi geriye düz metni çağırana hiç ulaşmamış bir
// anahtar kalırdı: kimse bilmediği için kullanılamaz, düz metni bir daha
// üretilemediği için tamamlanamaz — yalnızca elle silinecek bir çöp satır.
func TestAnahtarVeBaglariTekIslemdeYazilir(t *testing.T) {
	ctx := context.Background()
	repo := yeniDepo(t)
	canli := yeniKanal(ctx, t, repo)
	olu := yeniKanal(ctx, t, repo)
	require.NoError(t, repo.DeleteSalesChannel(ctx, olu.ID, time.Now().UTC()))

	kayit := anahtarKaydi(t)
	_, err := repo.CreateAPIKeyWithChannels(ctx, kayit, []string{canli.ID, olu.ID})
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err))

	// Tombstone bile kalmaz: işlem geri alındığında satır hiç var olmamıştır.
	assert.Equal(t, int64(0), sayim(ctx,
		t, `SELECT count(*) FROM api_key WHERE id = $1`, kayit.ID))
	assert.Equal(t, int64(0), sayim(ctx,
		t, `SELECT count(*) FROM api_key_sales_channel WHERE api_key_id = $1`, kayit.ID),
		"ilk bağ da geri alınmalı")
	_, err = repo.GetAPIKeyByHash(ctx, kayit.TokenHash)
	assert.True(t, errors.IsNotFound(err), "geri alınan anahtar hiçbir yerden okunamamalı")
}

// TestAnahtarVeBaglariBasariliYoldaBirlikteYazilir yazımın başarılı yolda hem
// anahtarı hem bağları bıraktığını kanıtlar.
func TestAnahtarVeBaglariBasariliYoldaBirlikteYazilir(t *testing.T) {
	ctx := context.Background()
	repo := yeniDepo(t)
	birinci := yeniKanal(ctx, t, repo)
	ikinci := yeniKanal(ctx, t, repo)

	key, err := repo.CreateAPIKeyWithChannels(ctx, anahtarKaydi(t), []string{birinci.ID, ikinci.ID})
	require.NoError(t, err)

	ids, err := repo.ChannelIDsOfKey(ctx, key.ID)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{birinci.ID, ikinci.ID}, ids)
}

// sadeDepo yalnızca [service.Repository] yüzeyini sunan bir sarmalayıcıdır.
//
// Gömülü arayüz yalnızca KENDİ metotlarını taşır: CreateAPIKeyWithChannels
// dışarıda kalır ve servisin tür doğrulaması başarısız olur. Böylece işlem
// açamayan bir depoyla çalışan TELAFİ yolu sınanabilir hâle gelir; o yol
// olmasaydı sahte depolarla kurulan her servis testi arkasında çöp anahtar
// bırakırdı.
type sadeDepo struct{ service.Repository }

// TestIslemsizDepodaBagKurulamazsaAnahtarGeriAlinir telafi yolunun anahtarı
// gerçekten kaldırdığını kanıtlar.
//
// Atomik depoda satır hiç oluşmaz; burada oluşur ve yumuşak silinir — geriye
// hiçbir yerden okunamayan bir mezar taşı kalır. İkisi de "kimsenin eline
// geçmemiş anahtar kullanılamaz" sözleşmesini tutar.
func TestIslemsizDepodaBagKurulamazsaAnahtarGeriAlinir(t *testing.T) {
	ctx := context.Background()
	repo := yeniDepo(t)
	olu := yeniKanal(ctx, t, repo)
	require.NoError(t, repo.DeleteSalesChannel(ctx, olu.ID, time.Now().UTC()))

	svc := service.New(sadeDepo{repo}, service.Options{JWTSecret: "yalnizca-test-icin-kullanilan-sir"})
	baslik := "telafi " + time.Now().UTC().Format(time.RFC3339Nano)

	_, plaintext, err := svc.CreateAPIKey(ctx, service.CreateAPIKeyInput{
		Type:            models.APIKeyPublishable,
		Title:           baslik,
		SalesChannelIDs: []string{olu.ID},
	})
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err))
	assert.Empty(t, plaintext, "başarısız çağrı düz metin sızdırmamalı")

	assert.Equal(t, int64(0), sayim(ctx,
		t, `SELECT count(*) FROM api_key WHERE title = $1 AND deleted_at IS NULL`, baslik),
		"bağ kurulamayan anahtar canlı kalmamalı")
}

// TestVeriIstisnasiIstemciHatasiUretir 22xxx sınıfının 500 değil 422 döndüğünü
// GERÇEK sunucuda kanıtlar.
//
// jsonb, metnin içindeki NUL baytının JSON kaçışını metne çeviremez ve 22P05
// üretir. Bu değer
// tümüyle İSTEMCİDEN gelir: metadata alanına ne yazılacağını çağıran seçer.
// Sınıf tanınmasaydı istemcinin yazdığı bir karakter sunucu hatası olarak
// raporlanır, çağıran da isteğini düzeltmek yerine tekrar denerdi.
func TestVeriIstisnasiIstemciHatasiUretir(t *testing.T) {
	ctx := context.Background()
	repo := yeniDepo(t)
	now := time.Now().UTC()

	_, err := repo.CreateSalesChannel(ctx, models.SalesChannel{
		ID:        models.NewSalesChannelID(now),
		Name:      "kanal " + now.Format(time.RFC3339Nano),
		Metadata:  map[string]any{"not": "\x00"},
		CreatedAt: now,
	})
	require.Error(t, err)

	var pgErr *pgconn.PgError
	require.True(t, errors.As(err, &pgErr))
	require.Equal(t, "22", pgErr.Code[:2], "beklenen sınıf veri istisnası (22xxx), gelen: %s", pgErr.Code)
	assert.True(t, errors.IsInvalid(err),
		"veri istisnası istemci hatası olmalı, tür: %s", errors.KindOf(err))
	assert.Equal(t, repository.CodeConstraintViolation, errors.CodeOf(err))
	assert.NotContains(t, err.Error(), "kısıt", "kısıt adı yokken mesaja yarım ek yapılmamalı")
}
