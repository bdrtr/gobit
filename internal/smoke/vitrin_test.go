//go:build smoke

package smoke

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ordermodels "github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/payment/manual"
)

// Bu dosya, VİTRİN YOLUNUN gerçek ikilide açık olduğunu sabitler: sepet aç ->
// satır ekle -> tamamla -> sipariş.
//
// # Neden bir smoke senaryosu, neden statik bir denetim değil
//
// internal/arch'taki [TestHerAkisBilesimKokundeKurulu] "akışların yapıcısı
// bileşim kökünde, main()'den erişilebilen bir yerde çağrılıyor" der ve
// kaynakta okunabilecek şey budur. Çağrının KOŞUP koşmadığını statik analiz
// yanıtlayamaz: kurulum bir koşulun ("if acik { … }") arkasına alındığında o
// denetim GEÇER, oysa çalışan ikilide sepete satır da eklenemez, sepet siparişe
// de çevrilemez — iki uç da 500 döner (cart modülü akışı çözemez ve KAPALI
// arızalanır). Yani bulunmuş arızanın ta kendisi, daha sinsi bir biçimde,
// değişmezin altından geçer.
//
// Bu senaryo o boşluğu yolu KULLANARAK kapatır. Bayrak, koşul, işlev değişkeni
// — kurulumu hangi biçimde kapatırsanız kapatın, sepet ucu cevap vermez ve test
// düşer. Karşılığında statik değişmezin verdiği şeyi veremez: kurulum satırı
// SİLİNDİĞİNDE bu test yalnızca "satır eklenemedi, 500" der; hangi paketin
// kurulmadığını söylemez ve
// bunu söyleyebilmek için docker, iki konteyner ve bir açılış gerektirir.
// İki katman bu yüzden birlikte durur; gerekçenin tamamı değişmezin
// godoc'undadır.
//
// # Neden internal/e2e yetmiyor
//
// internal/e2e/vitrin_akisi_test.go aynı zinciri HTTP'den sürer ama router'ı
// httptest ile kendisi kurar: cmd/server'ın kablolamasını, açılış sırasını ve
// gerçek süreci ATLAR. Kurulumu bir bayrağın arkasına almak o testi
// düşürmezdi — çünkü akışları o test kendi zemininde kuruyor. Buradaki süreç
// ise `go build` çıktısını çalıştırır, yani kararı main() verir.

// Vitrin senaryosunun ELLE hesaplanmış tutarları (minor unit).
//
// Bölge %20 (2000 baz puan) vergilidir ve kargo yöntemi seçilmemiştir:
//
//	32_000 × 2 = 64_000 ara toplam
//	64_000 × %20 = 12_800 vergi
//	64_000 - 0 + 12_800 + 0 = 76_800 genel toplam
//
// Sayılar internal/e2e'deki vitrin senaryosuyla BİLİNÇLİ olarak aynıdır: iki
// koşum arasında bir fark çıkarsa, farkın kaynağı hesap değil KABLOLAMA olur ve
// aranacak yer daralır.
const (
	vitrinBirimFiyat int64 = 32_000
	vitrinAdet       int64 = 2
	vitrinAraToplam  int64 = 64_000
	vitrinVergi      int64 = 12_800
	vitrinToplam     int64 = 76_800
	// vitrinVergiBps bölgenin yedek vergi oranıdır (2000 = %20).
	vitrinVergiBps int32 = 2_000
	// vitrinStok lokasyondaki fiziksel adettir; siparişin ayıracağından FAZLA
	// olmalı ki senaryo "stok yetmedi" ile değil, sınadığı sebeple düşsün.
	vitrinStok int64 = 5
)

// vitrinParaBirimi senaryonun bölge ve fiyat para birimidir.
//
// Kod region modülünün tohumunda kayıtlıdır; tohumlanmamış bir kod bölgeyi
// açılışta değil, ilk sepet isteğinde patlatırdı.
const vitrinParaBirimi = "TRY"

// vitrinVaryantBasligi katalogdaki varyantın başlığıdır.
//
// Satırın başlığı KATALOGDAN kopyalanır ve istemci başlık gönderemez; sabitin
// senaryoda geri okunması, kopyanın gerçekten yapıldığının kanıtıdır.
const vitrinVaryantBasligi = "Smoke Vitrin Ürünü"

// vitrinSatir sepet ve sipariş satırlarının senaryonun okuduğu alanlarıdır.
//
// Modüllerin DTO tipleri import EDİLMEZ; gerekçe [zarfVerisi] belgesindedir.
type vitrinSatir struct {
	ID        string `json:"id"`
	VariantID string `json:"variant_id"`
	Title     string `json:"title"`
	Quantity  int64  `json:"quantity"`
	UnitPrice int64  `json:"unit_price"`
	Subtotal  int64  `json:"subtotal"`
}

// vitrinSepet sepet gövdesinin senaryonun okuduğu alanlarıdır.
type vitrinSepet struct {
	ID           string        `json:"id"`
	RegionID     string        `json:"region_id"`
	CurrencyCode string        `json:"currency_code"`
	Subtotal     int64         `json:"subtotal"`
	TaxTotal     int64         `json:"tax_total"`
	Total        int64         `json:"total"`
	TotalsStale  bool          `json:"totals_stale"`
	Items        []vitrinSatir `json:"items"`
}

// vitrinTamamlamaSonucu POST /store/v1/carts/{id}/complete yanıtıdır.
type vitrinTamamlamaSonucu struct {
	OrderID      string `json:"order_id"`
	CartID       string `json:"cart_id"`
	CurrencyCode string `json:"currency_code"`
	Total        int64  `json:"total"`
}

// vitrinSiparis sipariş gövdesinin senaryonun okuduğu alanlarıdır.
type vitrinSiparis struct {
	ID           string        `json:"id"`
	Status       string        `json:"status"`
	CartID       string        `json:"cart_id"`
	CurrencyCode string        `json:"currency_code"`
	Email        string        `json:"email"`
	Subtotal     int64         `json:"subtotal"`
	TaxTotal     int64         `json:"tax_total"`
	Total        int64         `json:"total"`
	Items        []vitrinSatir `json:"items"`
}

// TestVitrinSepettenSipariseGercekSurecte vitrin yolunun gerçek ikilide AÇIK
// olduğunu kanıtlar.
//
// Zincir tümüyle HTTP'dir ve tek bir süreçte koşar: soğuk açılış -> yönetim
// kimliği -> satış kanalı + publishable anahtar -> bölge -> katalog (ürün,
// varyant, fiyat, stok) -> sepet -> satır -> tamamlama -> siparişin yönetim
// ucundan geri okunması.
//
// # Hangi mutasyonları yakalar
//
// Yolu kapatan HER değişikliği, biçiminden bağımsız olarak:
//
//   - akış kurulumunu "if false" ile kapatmak (satır ekleme 500),
//   - kurulum satırını silmek (aynı),
//   - cart modülünün route kaydını silmek (sepet açma 404),
//   - fiyatlandırıcıyı bozmak (satır eklenmez, fiyat katalogdan gelmez).
//
// Yakalayamadığı tek şey, yolun AÇIK ama denetimin göremediği bir biçimde
// kurulmuş olmasıdır — o soru statik değişmezin işidir ve gerekçe orada yazar.
//
// # Neden sipariş yönetim ucundan okunuyor
//
// Tamamlama yanıtındaki order_id tek başına "sipariş oluştu" demez: akış
// kimliği üretip kaydı yazamamış olabilir. Yönetim ucundan geri okumak, kaydın
// gerçekten kalıcı olduğunu ve tutarlarının sepetinkiyle aynı olduğunu
// üretimdeki koruma yığınının içinden doğrular.
func TestVitrinSepettenSipariseGercekSurecte(t *testing.T) {
	dsn := senaryoVeritabani(t)

	ayar := temelAyarlar(dsn, bosPort(t))
	ayar["ADMIN_BOOTSTRAP_EMAIL"] = tohumEposta
	ayar["ADMIN_BOOTSTRAP_PASSWORD"] = tohumParola

	s := sunucuBaslat(t, ayar)
	s.hazirBekle(acilisSuresi)

	jeton, _, vitrinAnahtari := yonetimZeminiKur(t, s, "Smoke Vitrin Kanalı")
	bolgeID := vitrinBolgesiAc(t, s, jeton)
	varyantID := vitrinKataloguKur(t, s, jeton)

	const eposta = "smoke-vitrin@ornek.test"
	sepetID := vitrinSepetiAc(t, s, vitrinAnahtari, bolgeID, eposta)

	satirYolu := "/store/v1/carts/" + sepetID + "/line-items"

	t.Run("satır eklenir ve fiyat KATALOGDAN gelir", func(t *testing.T) {
		// Gövdede fiyat ve başlık YOK: ikisini de sunucu yazar. Akış kurulu
		// değilse bu istek 500 alır ve senaryo tam burada durur.
		kod, govde := s.vitrinIste(http.MethodPost, satirYolu, vitrinAnahtari,
			map[string]any{"variant_id": varyantID, "quantity": vitrinAdet})
		require.Equal(t, http.StatusCreated, kod,
			"sepete satır eklenemedi. 500 + cart_module_setup_failed, akışın container'dan "+
				"ÇÖZÜLEMEDİĞİNİ söyler (kapalı arıza: fiyatı sunucu belirlemeden satır "+
				"yazılmaz). 404 ise ucun MOUNT EDİLMEDİĞİNİ söyler; ikisini durum kodu "+
				"ayırt eder. gövde: %s", govde)

		satir := zarfVerisi[vitrinSatir](t, govde)
		assert.Equal(t, vitrinBirimFiyat, satir.UnitPrice,
			"birim fiyat katalogdan gelmeli; istemci hiçbir fiyat göndermedi")
		assert.Equal(t, vitrinVaryantBasligi, satir.Title,
			"başlık da katalogdan kopyalanmalı; istemci başlık da göndermiyor")
		assert.Equal(t, vitrinAraToplam, satir.Subtotal,
			"satır ara toplamı hesap turunda yazılmalı")
	})

	t.Run("sepetin hesabı TAZE ve vergili döner", func(t *testing.T) {
		kod, govde := s.vitrinIste(http.MethodGet, "/store/v1/carts/"+sepetID, vitrinAnahtari, nil)
		require.Equal(t, http.StatusOK, kod, "sepet okunamadı; gövde: %s", govde)

		sepet := zarfVerisi[vitrinSepet](t, govde)
		assert.Equal(t, vitrinAraToplam, sepet.Subtotal)
		assert.Equal(t, vitrinVergi, sepet.TaxTotal,
			"vergi bölgenin oranıyla hesaplanmalı; hesap turu koşmasaydı sıfır kalırdı")
		assert.Equal(t, vitrinToplam, sepet.Total)
		assert.False(t, sepet.TotalsStale,
			"satır eklendikten sonra toplamlar TAZE olmalı; bayat toplam sipariş edilemez")
		require.Len(t, sepet.Items, 1, "sepette tek satır olmalı")
	})

	var siparisID string

	t.Run("sepet siparişe çevrilir", func(t *testing.T) {
		kod, govde := s.vitrinIste(http.MethodPost, "/store/v1/carts/"+sepetID+"/complete",
			vitrinAnahtari, map[string]any{
				"payment_provider_id": manual.ID,
				"payment_data":        map[string]any{manual.DataKeyOutcome: manual.OutcomeAuthorize},
				"expected_total":      vitrinToplam,
			})
		require.Equal(t, http.StatusOK, kod,
			"sepet tamamlanamadı. 500 + cart_module_setup_failed, sipariş akışının "+
				"container'dan çözülemediğini söyler; 404 ise tamamlama ucunun mount "+
				"edilmediğini. Durum kodu hangisi olduğunu söyler. gövde: %s", govde)

		sonuc := zarfVerisi[vitrinTamamlamaSonucu](t, govde)
		require.NotEmpty(t, sonuc.OrderID, "yanıt siparişin kimliğini taşımalı; gövde: %s", govde)
		assert.Equal(t, sepetID, sonuc.CartID, "sonuç doğduğu sepeti belgelemeli")
		assert.Equal(t, vitrinParaBirimi, sonuc.CurrencyCode)
		assert.Equal(t, vitrinToplam, sonuc.Total,
			"tahsil edilen tutar ELLE hesaplanan genel toplam olmalı")

		siparisID = sonuc.OrderID
	})

	t.Run("sipariş kalıcıdır ve tutarları sepetinkiyle aynıdır", func(t *testing.T) {
		require.NotEmpty(t, siparisID, "önceki adım sipariş üretmedi; bu adım anlamsız")

		kod, govde := s.yonetimIste(http.MethodGet, "/admin/v1/orders/"+siparisID, jeton, nil)
		require.Equal(t, http.StatusOK, kod, "sipariş yönetim ucundan okunamadı; gövde: %s", govde)

		siparis := zarfVerisi[vitrinSiparis](t, govde)
		assert.Equal(t, string(ordermodels.OrderPending), siparis.Status)
		assert.Equal(t, sepetID, siparis.CartID, "sipariş doğduğu sepeti belgelemeli")
		assert.Equal(t, eposta, siparis.Email,
			"iletişim adresi SEPETTEN gelmeli; tamamlama gövdesi e-posta taşımıyor")
		assert.Equal(t, vitrinAraToplam, siparis.Subtotal)
		assert.Equal(t, vitrinVergi, siparis.TaxTotal)
		assert.Equal(t, vitrinToplam, siparis.Total)

		require.Len(t, siparis.Items, 1, "sepetin tek satırı siparişe tek satır olarak geçmeli")
		assert.Equal(t, vitrinBirimFiyat, siparis.Items[0].UnitPrice,
			"siparişteki birim fiyat da katalogdan gelen fiyat olmalı")
		assert.Equal(t, vitrinVaryantBasligi, siparis.Items[0].Title)
		assert.Equal(t, vitrinAdet, siparis.Items[0].Quantity)
	})

	t.Run("tamamlanan sepete ikinci satır eklenemez", func(t *testing.T) {
		// Sepetin KAPANDIĞININ kanıtı: kapanmasaydı aynı sepet ikinci bir
		// siparişe kaynak olurdu. İddia sepetin bayrağına değil DAVRANIŞINA
		// bakar, çünkü vitrin istemcisinin gördüğü şey budur.
		kod, govde := s.vitrinIste(http.MethodPost, satirYolu, vitrinAnahtari,
			map[string]any{"variant_id": varyantID, "quantity": int64(1)})
		assert.Equal(t, http.StatusConflict, kod,
			"tamamlanmış sepet değiştirilememeli; gövde: %s", govde)
	})
}

// vitrinBolgesiAc senaryonun vergili bölgesini açar ve kimliğini döner.
//
// Bölgeye ÜLKE bağlanmaz ve bu bilinçlidir: ülkesi tek bir koda çözülemeyen
// bölgede vergi, tax modülü yerine bölgenin kendi oranıyla hesaplanır
// (bkz. workflows/cart countryForRegion). Senaryonun sınadığı şey verginin
// hangi modülden geldiği değil, hesap turunun KOŞTUĞUDUR; vergi bölgesi
// kurmak, o soruyla ilgisi olmayan üç isteği daha senaryoya eklerdi.
func vitrinBolgesiAc(t *testing.T, s *surec, jeton string) string {
	t.Helper()

	kod, govde := s.yonetimIste(http.MethodPost, "/admin/v1/regions", jeton, map[string]any{
		"name":            "Smoke Vitrin Bölgesi",
		"currency_code":   vitrinParaBirimi,
		"automatic_taxes": true,
		"tax_rate_bps":    vitrinVergiBps,
	})
	require.Equal(t, http.StatusCreated, kod, "bölge açılamadı; gövde: %s", govde)

	bolge := zarfVerisi[struct {
		ID string `json:"id"`
	}](t, govde)
	require.NotEmpty(t, bolge.ID, "bölge kimlik dönmeli; gövde: %s", govde)

	return bolge.ID
}

// vitrinKataloguKur fiyatı VE stoğu olan bir varyant kurar; varyantın kimliğini
// döner.
//
// Kurulum beş parçadır ve beşi de YÖNETİM UÇLARINDAN geçer: ürün + varyant,
// fiyat kümesi ve varyanta bağı, stok lokasyonu, stok kalemi ve varyanta bağı,
// lokasyondaki fiziksel adet. Bağlar zorunludur — fiyat bağı olmayan varyant
// sepete giremez, stok bağı olmayan varyant ise akış tarafından "stoksuz"
// sayılır ve sepet hiç sipariş olamaz.
func vitrinKataloguKur(t *testing.T, s *surec, jeton string) string {
	t.Helper()

	varyantID := vitrinVaryantiAc(t, s, jeton)
	vitrinFiyatiBagla(t, s, jeton, varyantID)
	vitrinStogunuBagla(t, s, jeton, varyantID)

	return varyantID
}

// vitrinVaryantiAc bir ürün ve altında bir varyant açar; varyantın kimliğini
// döner.
func vitrinVaryantiAc(t *testing.T, s *surec, jeton string) string {
	t.Helper()

	kod, govde := s.yonetimIste(http.MethodPost, "/admin/v1/products", jeton, map[string]any{
		"handle": "smoke-vitrin-urunu",
		"title":  vitrinVaryantBasligi,
		"status": "published",
	})
	require.Equal(t, http.StatusCreated, kod, "ürün açılamadı; gövde: %s", govde)

	urunID := zarfVerisi[struct {
		ID string `json:"id"`
	}](t, govde).ID
	require.NotEmpty(t, urunID, "ürün kimlik dönmeli; gövde: %s", govde)

	kod, govde = s.yonetimIste(http.MethodPost, "/admin/v1/products/"+urunID+"/variants", jeton,
		map[string]any{"title": vitrinVaryantBasligi})
	require.Equal(t, http.StatusCreated, kod, "varyant açılamadı; gövde: %s", govde)

	varyantID := zarfVerisi[struct {
		ID string `json:"id"`
	}](t, govde).ID
	require.NotEmpty(t, varyantID, "varyant kimlik dönmeli; gövde: %s", govde)

	return varyantID
}

// vitrinFiyatiBagla bir fiyat kümesi açar ve varyanta bağlar.
func vitrinFiyatiBagla(t *testing.T, s *surec, jeton, varyantID string) {
	t.Helper()

	kod, govde := s.yonetimIste(http.MethodPost, "/admin/v1/price-sets", jeton, map[string]any{
		"prices": []map[string]any{
			{"currency_code": vitrinParaBirimi, "amount": vitrinBirimFiyat, "min_quantity": 1},
		},
	})
	require.Equal(t, http.StatusCreated, kod, "fiyat kümesi açılamadı; gövde: %s", govde)

	kumeID := zarfVerisi[struct {
		ID string `json:"id"`
	}](t, govde).ID
	require.NotEmpty(t, kumeID, "fiyat kümesi kimlik dönmeli; gövde: %s", govde)

	kod, govde = s.yonetimIste(http.MethodPut, "/admin/v1/variants/"+varyantID+"/price-set", jeton,
		map[string]any{"price_set_id": kumeID})
	require.Equal(t, http.StatusOK, kod,
		"varyant fiyat kümesine bağlanamadı; bağ olmadan akış fiyatı bulamaz. gövde: %s", govde)
}

// vitrinStogunuBagla bir lokasyon ve stok kalemi açar, kalemi varyanta bağlar ve
// lokasyondaki fiziksel adedi yazar.
func vitrinStogunuBagla(t *testing.T, s *surec, jeton, varyantID string) {
	t.Helper()

	kod, govde := s.yonetimIste(http.MethodPost, "/admin/v1/stock-locations", jeton,
		map[string]any{"name": "Smoke Vitrin Deposu"})
	require.Equal(t, http.StatusCreated, kod, "stok lokasyonu açılamadı; gövde: %s", govde)

	lokasyonID := zarfVerisi[struct {
		ID string `json:"id"`
	}](t, govde).ID
	require.NotEmpty(t, lokasyonID, "lokasyon kimlik dönmeli; gövde: %s", govde)

	kod, govde = s.yonetimIste(http.MethodPost, "/admin/v1/inventory-items", jeton,
		map[string]any{"sku": "SMOKE-VITRIN-1", "title": vitrinVaryantBasligi})
	require.Equal(t, http.StatusCreated, kod, "stok kalemi açılamadı; gövde: %s", govde)

	kalemID := zarfVerisi[struct {
		ID string `json:"id"`
	}](t, govde).ID
	require.NotEmpty(t, kalemID, "stok kalemi kimlik dönmeli; gövde: %s", govde)

	kod, govde = s.yonetimIste(http.MethodPut, "/admin/v1/variants/"+varyantID+"/inventory-item",
		jeton, map[string]any{"inventory_item_id": kalemID})
	require.Equal(t, http.StatusOK, kod,
		"varyant stok kalemine bağlanamadı; bağ olmadan akış varyantı stoksuz sayar. gövde: %s",
		govde)

	kod, govde = s.yonetimIste(http.MethodPost, "/admin/v1/inventory-items/"+kalemID+"/levels",
		jeton, map[string]any{"location_id": lokasyonID, "stocked_quantity": vitrinStok})
	require.Equal(t, http.StatusOK, kod, "stok seviyesi yazılamadı; gövde: %s", govde)
}

// vitrinSepetiAc vitrin ucundan MİSAFİR bir sepet açar ve kimliğini döner.
//
// Sepet müşterisizdir ve bu bilinçlidir: misafir alışverişi vitrinin varsayılan
// yoludur ve bir müşteri kaydı, senaryonun sınadığı zincire hiçbir şey
// eklemeden iki istek daha eklerdi. İletişim adresi yine de verilir — sipariş
// onu sepetten kopyalar ve kopyanın yapıldığı senaryoda doğrulanır.
func vitrinSepetiAc(t *testing.T, s *surec, anahtar, bolgeID, eposta string) string {
	t.Helper()

	// Gövdede para birimi YOK: onu sunucu bölgeden türetir. Göndermek 422
	// alırdı — fiyat yetkisiyle aynı kural (bkz. CHANGELOG, "Para birimi
	// yetkisi istemciden alındı").
	kod, govde := s.vitrinIste(http.MethodPost, "/store/v1/carts", anahtar, map[string]any{
		"region_id": bolgeID,
		"email":     eposta,
	})
	require.Equal(t, http.StatusCreated, kod,
		"sepet açılamadı; 404 vitrin uçlarının mount edilmediğini gösterir, "+
			"422 ise gövdenin sunucunun belirlediği bir alanı taşıdığını. gövde: %s", govde)

	sepet := zarfVerisi[vitrinSepet](t, govde)
	require.NotEmpty(t, sepet.ID, "sepet kimlik dönmeli; gövde: %s", govde)
	require.Equal(t, bolgeID, sepet.RegionID,
		"sepet istenen bölgede açılmalı; gövde: %s", govde)

	return sepet.ID
}
