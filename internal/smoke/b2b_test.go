//go:build smoke

package smoke

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/db"
	"github.com/bdrtr/gobit/internal/modules/b2b"
)

// b2bHarcamaLimiti senaryonun çalışana verdiği ilk harcama limitidir
// (minor unit). Değerin kendisi önemsizdir; önemli olan, vitrinden AYNI sayının
// geri okunabilmesidir.
const b2bHarcamaLimiti int64 = 500_000

// b2bYeniLimit yönetim ucundan yapılan güncellemenin yazdığı limittir.
//
// İlkinden FARKLI olması senaryonun koşuludur: aynı sayı yazılsaydı, vitrinin
// güncellenmiş kaydı mı yoksa eski kaydı mı okuduğu ayırt edilemezdi.
const b2bYeniLimit int64 = 250_000

// b2bStoreEmployee vitrindeki çalışan kaydının senaryonun okuduğu alanlarıdır.
//
// Modülün DTO tipi import EDİLMEZ; gerekçe [zarfVerisi] belgesindedir.
type b2bStoreEmployee struct {
	ID                       string     `json:"id"`
	CompanyID                string     `json:"company_id"`
	CustomerID               string     `json:"customer_id"`
	SpendingLimit            *int64     `json:"spending_limit"`
	SpendingLimitResetPeriod string     `json:"spending_limit_reset_period"`
	SpendingWindowStart      *time.Time `json:"spending_window_start"`
	IsCompanyAdmin           bool       `json:"is_company_admin"`
}

// b2bMusteriAc yönetim ucundan bir müşteri açar ve kimliğini döner.
//
// Müşteri b2b'nin DEĞİL customer modülünün kaydıdır ve çalışan bağı ancak
// gerçek bir "cust_" kimliğiyle kurulabilir (bkz. b2b service requireID).
// Uydurulmuş bir kimlikle çalışılsaydı senaryo, iki modülü birbirine bağlayan
// link katmanını hiç sürmemiş olurdu.
func b2bMusteriAc(t *testing.T, s *surec, jeton, eposta string) string {
	t.Helper()

	kod, govde := s.yonetimIste(http.MethodPost, "/admin/v1/customers", jeton,
		map[string]any{"email": eposta, "first_name": "Smoke", "last_name": "B2B"})
	require.Equal(t, http.StatusCreated, kod, "müşteri açılamadı; gövde: %s", govde)

	musteri := zarfVerisi[struct {
		ID string `json:"id"`
	}](t, govde)
	require.NotEmpty(t, musteri.ID, "müşteri kimlik dönmeli; gövde: %s", govde)

	return musteri.ID
}

// b2bSirketAc yönetim ucundan bir şirket açar ve kimliğini döner.
func b2bSirketAc(t *testing.T, s *surec, jeton, ad, eposta, periyot string) string {
	t.Helper()

	kod, govde := s.yonetimIste(http.MethodPost, "/admin/v1/b2b/companies", jeton, map[string]any{
		"name":                        ad,
		"email":                       eposta,
		"currency_code":               "TRY",
		"spending_limit_reset_period": periyot,
	})
	require.Equal(t, http.StatusCreated, kod, "şirket açılamadı; gövde: %s", govde)

	sirket := zarfVerisi[struct {
		ID string `json:"id"`
	}](t, govde)
	require.NotEmpty(t, sirket.ID, "şirket kimlik dönmeli; gövde: %s", govde)

	return sirket.ID
}

// b2bVitrinCalisani vitrin ucundan müşterinin KENDİ çalışan kaydını okur.
func b2bVitrinCalisani(t *testing.T, s *surec, anahtar, musteriID string) b2bStoreEmployee {
	t.Helper()

	kod, govde := s.vitrinIste(http.MethodGet,
		"/store/v1/b2b/customers/"+musteriID+"/employee", anahtar, nil)
	require.Equal(t, http.StatusOK, kod, "vitrin çalışan kaydı okunamadı; gövde: %s", govde)

	return zarfVerisi[b2bStoreEmployee](t, govde)
}

// b2bSemasiniDogrula b2b tablolarının soğuk açılışta kurulduğunu doğrular.
//
// Sorgu tabloların KENDİSİNE bakar, bir uca değil: bir modülün migration'ı
// açılışa bağlanmayı unutsa da uçları mount edilebilir ve ilk istek "relation
// does not exist" ile ölürdü — yani okuma yüzeyi üzerinden bakan bir test,
// arızayı ancak o uca gidildiğinde ve anlaşılmaz bir hatayla görürdü.
//
// Versiyon defteri de denetlenir çünkü tablonun VARLIĞI tek başına yetmez:
// yarım kalmış bir migration da tablo bırakabilir. "dirty" bayrağı, o durumun
// tek görünür izidir.
func b2bSemasiniDogrula(t *testing.T, dsn string) {
	t.Helper()

	havuz, err := db.New(t.Context(), db.DefaultConfig(dsn), nil)
	require.NoError(t, err, "senaryo veritabanına bağlanılamadı")
	defer havuz.Close()

	defter, err := db.MigrationsTable(b2b.ModuleName)
	require.NoError(t, err, "b2b versiyon tablosunun adı üretilemedi")

	for _, tablo := range []string{"b2b_company", "b2b_company_employee", defter} {
		var varMi bool
		require.NoError(t,
			havuz.Pool().QueryRow(t.Context(), "SELECT to_regclass($1) IS NOT NULL", tablo).Scan(&varMi),
			"%s tablosu sorgulanamadı", tablo)
		assert.True(t, varMi,
			"soğuk açılış %q tablosunu kurmalı: b2b YENİ bir modüldür ve "+
				"migration'ı açılışa bağlanmamışsa tablo hiç yaratılmaz", tablo)
	}

	var (
		versiyon int64
		kirli    bool
	)
	require.NoError(t,
		havuz.Pool().QueryRow(t.Context(), "SELECT version, dirty FROM "+defter).Scan(&versiyon, &kirli),
		"b2b versiyon defteri okunamadı")

	assert.Positive(t, versiyon, "b2b migration'ı uygulanmış olmalı")
	assert.False(t, kirli, "b2b migration'ı yarım kalmamalı (dirty)")
}

// TestB2BUctanUcaGercekSurecte F senaryosudur: B2B modülü gerçek binary
// üzerinde, gerçek açılış sırasıyla çalışır.
//
// # Neden gerçek süreç
//
// b2b Bölüm 10'da eklendi ve GERÇEK BINARY ÜZERİNDE hiç koşmadı. internal/e2e
// modülün akışını kanıtlar ama servisleri KENDİ kurar: bileşim kökündeki
// registry.Add satırını, açılıştaki migration sırasını, koruma yığınını ve
// gerçek ağı atlar. cmd/server'ın kendi belgesi bu boşluğun bedelini yazıyor:
// "buraya EKLENMEYEN bir modül hiçbir kurulumda YOKTUR" — ve b2b'nin harcama
// limiti tam olarak böyle bir kez kaybolmuştu.
//
// Senaryo TEK süreç açar ve şu zinciri sürer: soğuk açılış → migration →
// yönetim kimliği → satış kanalı → publishable anahtar → müşteri → şirket →
// çalışan → vitrinden geri okuma.
//
// # Bu senaryo limitin KURALINI sınar, UYGULANMASINI değil
//
// Limitin kural tarafı (limit kaç, hangi pencerede, hangi para biriminde)
// vitrinden geri okunarak burada kanıtlanır. Limitin uygulanması ise sipariş
// açılırken olur (order service CreateOrder) ve oraya ancak sepeti siparişe
// çeviren yoldan gidilir.
//
// Bu godoc bir zamanlar "o yol çalışan binary'de YOK" diyordu ve haklıydı:
// cmd/server yalnızca saga MOTORUNU container'a bırakıyor, cart ile checkout
// akışlarının yapıcısını çağıran tek yer internal/e2e oluyordu. Arıza o günden
// beri kapandı — akışlar bileşim kökünde kuruluyor ve vitrin yolunun gerçek
// süreçte AÇIK olduğu bu pakette sabitlendi
// (bkz. [TestVitrinSepettenSipariseGercekSurecte]).
//
// Yani limit artık bu süreçte de tetiklenebilir; tetiklenmiyor olması bir
// imkânsızlık değil, bilinçli bir KAPSAM kararıdır: senaryoyu oraya taşımak
// katalog fikstürünün (ürün, fiyat, stok, lokasyon) tamamını ikinci kez
// kurmayı ve ikinci bir sunucu süreci açmayı gerektirirdi. Kuralın
// UYGULANDIĞI, aynı adımlarla e2e'de kanıtlanır
// (internal/e2e/b2b_test.go): limiti aşan alışveriş siparişe dönmez, para
// çekilmez, stok hareketsiz kalır. Reddin GÖVDEYE kadar geldiği — yani
// vitrinin "limitiniz yetmedi" ile "tekrar deneyin"i ayırt edebildiği — orada
// HTTP uçlarından ayrıca sınanır (TestVitrinB2BLimitReddiSebebiniBildirir).
func TestB2BUctanUcaGercekSurecte(t *testing.T) {
	dsn := senaryoVeritabani(t)

	ayar := temelAyarlar(dsn, bosPort(t))
	ayar["ADMIN_BOOTSTRAP_EMAIL"] = tohumEposta
	ayar["ADMIN_BOOTSTRAP_PASSWORD"] = tohumParola

	s := sunucuBaslat(t, ayar)
	s.hazirBekle(acilisSuresi)

	t.Run("soğuk açılış b2b şemasını kurar", func(t *testing.T) {
		b2bSemasiniDogrula(t, dsn)
	})

	jeton, _, vitrinAnahtari := yonetimZeminiKur(t, s, "Smoke B2B Kanalı")

	musteriID := b2bMusteriAc(t, s, jeton, "smoke-b2b@ornek.test")
	sirketID := b2bSirketAc(t, s, jeton, "Smoke B2B A.Ş.", "smoke-b2b-sirket@ornek.test", "monthly")

	limit := b2bHarcamaLimiti
	kod, govde := s.yonetimIste(http.MethodPost, "/admin/v1/b2b/employees", jeton, map[string]any{
		"company_id":       sirketID,
		"customer_id":      musteriID,
		"spending_limit":   limit,
		"is_company_admin": true,
	})
	require.Equal(t, http.StatusCreated, kod, "çalışan eklenemedi; gövde: %s", govde)

	calisanID := zarfVerisi[struct {
		ID string `json:"id"`
	}](t, govde).ID
	require.NotEmpty(t, calisanID, "çalışan kimlik dönmeli; gövde: %s", govde)

	t.Run("vitrin müşterinin kendi şirketini döner", func(t *testing.T) {
		kod, govde := s.vitrinIste(http.MethodGet,
			"/store/v1/b2b/customers/"+musteriID+"/company", vitrinAnahtari, nil)
		require.Equal(t, http.StatusOK, kod, "vitrin şirket ucu 200 dönmeli; gövde: %s", govde)

		sirket := zarfVerisi[struct {
			ID                       string `json:"id"`
			Name                     string `json:"name"`
			CurrencyCode             string `json:"currency_code"`
			SpendingLimitResetPeriod string `json:"spending_limit_reset_period"`
		}](t, govde)

		assert.Equal(t, sirketID, sirket.ID,
			"vitrin, çalışan kaydından ÇÖZÜLEN şirketi dönmeli; gövde: %s", govde)
		assert.Equal(t, "TRY", sirket.CurrencyCode,
			"para birimi normalize edilerek saklanmalı; gövde: %s", govde)
		assert.Equal(t, "monthly", sirket.SpendingLimitResetPeriod,
			"sıfırlama periyodu yönetimden yazıldığı gibi okunmalı; gövde: %s", govde)
	})

	t.Run("vitrin harcama kuralını geri okur", func(t *testing.T) {
		calisan := b2bVitrinCalisani(t, s, vitrinAnahtari, musteriID)

		assert.Equal(t, calisanID, calisan.ID, "vitrin aynı çalışan kaydını dönmeli")
		assert.Equal(t, musteriID, calisan.CustomerID,
			"müşteri kimliği link katmanından dolmalı; boş görünmesi bağın hiç "+
				"kurulmadığı anlamına gelir")
		require.NotNil(t, calisan.SpendingLimit, "harcama limiti dolu olmalı")
		assert.Equal(t, b2bHarcamaLimiti, *calisan.SpendingLimit,
			"vitrin, yönetimden yazılan limitin AYNISINI dönmeli")
		assert.Equal(t, "monthly", calisan.SpendingLimitResetPeriod,
			"periyot çalışanın kendi kaydından değil ŞİRKETTEN türetilir")

		// Pencere TAKVİMDEN gelir (bkz. models.SpendingResetPeriod.WindowStart):
		// aylık limit ayın 1'inde, UTC gece yarısında başlar. İddia bugünün
		// tarihine değil pencerenin KENDİ tarihine bakar; aksi hâlde ay
		// devrinde koşan bir test kendi kendine düşerdi.
		require.NotNil(t, calisan.SpendingWindowStart,
			"aylık periyotta pencere başlangıcı dolu olmalı")

		pencere := calisan.SpendingWindowStart.UTC()
		assert.Equal(t,
			time.Date(pencere.Year(), pencere.Month(), 1, 0, 0, 0, 0, time.UTC), pencere,
			"aylık pencere, ayın ilk anında (UTC) başlamalı")
		assert.False(t, pencere.After(time.Now().UTC()),
			"pencere gelecekte başlayamaz")
	})

	t.Run("limitin güncellenmesi vitrine yansır", func(t *testing.T) {
		// Kuralın CANLI okunduğunun kanıtı budur: vitrin, çalışan kaydını
		// açılışta bir kez okuyup önbelleğe alsaydı bu alt test düşerdi ve
		// operatörün yükselttiği bir limit hiçbir zaman uygulanmazdı.
		yeni := b2bYeniLimit
		kod, govde := s.yonetimIste(http.MethodPut, "/admin/v1/b2b/employees/"+calisanID, jeton,
			map[string]any{"spending_limit": yeni})
		require.Equal(t, http.StatusOK, kod, "çalışan güncellenemedi; gövde: %s", govde)

		calisan := b2bVitrinCalisani(t, s, vitrinAnahtari, musteriID)
		require.NotNil(t, calisan.SpendingLimit, "güncellenmiş limit dolu olmalı")
		assert.Equal(t, b2bYeniLimit, *calisan.SpendingLimit,
			"vitrin GÜNCEL limiti dönmeli")
	})

	t.Run("bir müşteri ikinci şirkete çalışan olarak eklenemez", func(t *testing.T) {
		// Kural uygulamada değil VERİTABANINDA durur (link tablosunun benzersiz
		// indeksi, bkz. b2b service Definitions). Bu alt test onu gerçek
		// şemaya karşı sürer: indeks soğuk açılışta kurulmamış olsaydı istek
		// 201 döner ve müşterinin hangi şirketin limitine tabi olduğu
		// belirsizleşirdi — belirsizlik, kuralın hiç uygulanmaması demektir.
		digerSirket := b2bSirketAc(t, s, jeton,
			"Smoke B2B İkinci A.Ş.", "smoke-b2b-ikinci@ornek.test", "never")

		kod, govde := s.yonetimIste(http.MethodPost, "/admin/v1/b2b/employees", jeton,
			map[string]any{"company_id": digerSirket, "customer_id": musteriID})

		assert.Equal(t, http.StatusConflict, kod,
			"aynı müşteri ikinci şirkete bağlanamamalı; gövde: %s", govde)
	})

	t.Run("anahtarsız vitrin isteği 401 döner", func(t *testing.T) {
		kod, govde := s.vitrinIste(http.MethodGet,
			"/store/v1/b2b/customers/"+musteriID+"/employee", "", nil)

		assert.Equal(t, http.StatusUnauthorized, kod,
			"publishable anahtarsız vitrin isteği reddedilmeli; gövde: %s", govde)
	})

	t.Run("kimliksiz yönetim isteği 401 döner", func(t *testing.T) {
		// Yolun VARLIĞI da sızmamalı: koruma, route çözümünden önce çalışır.
		kod, govde := s.iste(http.MethodGet, "/admin/v1/b2b/companies", "")

		assert.Equal(t, http.StatusUnauthorized, kod,
			"kimliksiz b2b yönetim isteği reddedilmeli; gövde: %s", govde)
	})
}
