//go:build integration

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/internal/core/http"
	b2bmodels "github.com/bdrtr/gobit/internal/modules/b2b/models"
	ordermodels "github.com/bdrtr/gobit/internal/modules/order/models"
	ordersvc "github.com/bdrtr/gobit/internal/modules/order/service"
	"github.com/bdrtr/gobit/internal/modules/payment/manual"
)

// Bu dosya akışların ÜRETİM İKİLİSİNE bağlandığını kanıtlar.
//
// # Neden diğer e2e testleri yetmiyordu
//
// Faz 5-7'nin bütün senaryoları akışları DOĞRUDAN çağırıyordu
// (akislar.AddLineItem, siparisAkislari.CompleteCart). Bu, akışların doğru
// hesap yaptığını kanıtlar ama HİÇ KİMSENİN onları çağırmadığı bir kurulumda
// da yeşil kalır — ve tam olarak öyleydi: cmd/server yalnızca saga MOTORUNU
// kaydediyordu, akışların kendisini üretim kodunda çağıran tek satır yoktu.
// Çalışan ikilide sepeti siparişe çeviren yol YOKTU; ödeme, kargo, checkout
// promosyonu, order.placed bildirimi ve b2b harcama limiti ERİŞİLEMEZDİ.
//
// Buradaki senaryolar bu yüzden akışa hiç dokunmaz: her adım HTTP ucundan,
// publishable anahtarla, üretimdeki koruma yığınının içinden geçer. Bir gün
// kayıt satırı silinirse (ya da modülün ad sabiti kayarsa) bu testler düşer;
// akışları doğrudan çağıran testler düşmezdi.
//
// # İkinci kanıt: FİYAT YETKİSİ
//
// POST /store/v1/carts/{id}/line-items gövdesi "unit_price" alıyor ve cart
// servisi onu OLDUĞU GİBİ yazıyordu; yalnızca aralık denetleniyor, doğruluğu
// denetlenmiyordu. Vitrinin kimliği publishable anahtardır ve tarayıcıda
// durur — yani bu, herkesin erişebildiği bir "kendi fiyatını yaz" ucuydu.
// [TestVitrinIstemciFiyatiniReddeder] o kapının kapandığını, mutlu yol testi
// ise fiyatın gerçekten katalogdan geldiğini gösterir.

// Vitrin senaryosunun ELLE hesaplanmış tutarları.
//
// Bölge %20 (2000 baz puan) vergilidir ve kargo yöntemi seçilmemiştir:
//
//	32_000 × 2 = 64_000 ara toplam
//	64_000 × %20 = 12_800 vergi
//	64_000 - 0 + 12_800 + 0 = 76_800 genel toplam
const (
	vitrinBirimFiyat    int64 = 32_000
	vitrinAdet          int64 = 2
	vitrinAraToplam     int64 = 64_000
	vitrinVergi         int64 = 12_800
	vitrinToplam        int64 = 76_800
	vitrinBaslangicStok int64 = 5
	// vitrinKalanStok tahsilat sonrası beklenen fiziksel adettir: 5 - 2.
	vitrinKalanStok int64 = 3
)

// TestVitrinSepettenSiparise sepetin ÜRETİM UÇLARINDAN geçerek siparişe
// döndüğünü kanıtlar.
//
// Zincir tümüyle HTTP'dir: sepet aç -> satır ekle (fiyatsız) -> sepeti oku ->
// tamamla. Ardından sonuç, modüllerin KENDİ verisinden doğrulanır — sipariş
// gerçekten açıldı mı, para çekildi mi, stok düştü mü, sepet kapandı mı,
// "order.placed" yayımlandı mı.
func TestVitrinSepettenSiparise(t *testing.T) {
	ctx := t.Context()

	musteriID, eposta := yeniMusteri(ctx, t)
	const varyantBaslik = "E2E Vitrin Ürünü"
	varyantID, stokKalemID := yeniStokluVaryant(ctx, t, varyantBaslik, map[string]int64{
		vergiliParaBirimi: vitrinBirimFiyat,
	}, vitrinBaslangicStok)

	sepetID := vitrinSepetiAc(t, musteriID, eposta)

	// --- satır: gövdede fiyat YOK, fiyatı sunucu belirler ---

	ekle := vitrinIstegi(t, http.MethodPost, "/store/v1/carts/"+sepetID+"/line-items",
		fmt.Sprintf(`{"variant_id":%q,"quantity":%d}`, varyantID, vitrinAdet))
	require.Equal(t, http.StatusCreated, ekle.Code, "gövde: %s", ekle.Body.String())

	satir := vitrinVeri(t, ekle)
	assert.InDelta(t, float64(vitrinBirimFiyat), satir["unit_price"], 0,
		"birim fiyat KATALOGDAN gelmeli; istemci hiçbir fiyat göndermedi")
	assert.Equal(t, varyantBaslik, satir["title"],
		"başlık da katalogdan kopyalanmalı; istemci başlık da göndermiyor")
	assert.InDelta(t, float64(vitrinAraToplam), satir["subtotal"], 0,
		"satır ara toplamı hesap turunda yazılmalı")

	// --- sepet: toplamlar HTTP'den okunduğunda hesaplanmış ve TAZE olmalı ---

	oku := vitrinIstegi(t, http.MethodGet, "/store/v1/carts/"+sepetID, "")
	require.Equal(t, http.StatusOK, oku.Code, "gövde: %s", oku.Body.String())

	sepet := vitrinVeri(t, oku)
	assert.InDelta(t, float64(vitrinAraToplam), sepet["subtotal"], 0)
	assert.InDelta(t, float64(vitrinVergi), sepet["tax_total"], 0,
		"vergi bölgenin oranıyla hesaplanmalı; hesap turu koşmasaydı sıfır kalırdı")
	assert.InDelta(t, float64(vitrinToplam), sepet["total"], 0)
	assert.Equal(t, false, sepet["totals_stale"],
		"satır eklendikten sonra toplamlar TAZE olmalı; bayat toplam sipariş edilemez")

	require.Equal(t, vitrinBaslangicStok, satilabilirAdet(ctx, t, stokKalemID),
		"sepete satır eklemek stok AYIRMAMALI")

	// --- tamamlama ---

	tamam := vitrinIstegi(t, http.MethodPost, "/store/v1/carts/"+sepetID+"/complete",
		vitrinTamamlamaGovdesi(t, vitrinToplam))
	require.Equal(t, http.StatusOK, tamam.Code, "gövde: %s", tamam.Body.String())

	sonuc := vitrinVeri(t, tamam)
	siparisID, _ := sonuc["order_id"].(string)
	require.NotEmpty(t, siparisID, "yanıt siparişin kimliğini taşımalı")
	assert.Equal(t, sepetID, sonuc["cart_id"])
	assert.Equal(t, vergiliParaBirimi, sonuc["currency_code"])
	assert.InDelta(t, float64(vitrinToplam), sonuc["total"], 0,
		"tahsil edilen tutar ELLE hesaplanan genel toplam olmalı")

	// Yanıt İÇ kimlikleri taşımaz: ödeme oturumu, koleksiyon ve rezervasyon
	// kimlikleri mağaza istemcisinin hiçbir ucundan kullanamayacağı iç yapıdır.
	for _, alan := range []string{
		"payment_id", "payment_session_id", "payment_collection_id",
		"reservation_ids", "warnings",
	} {
		assert.NotContains(t, sonuc, alan, "%s vitrin yanıtında yer almamalı", alan)
	}

	// --- sonuç modüllerin KENDİ verisinden doğrulanır ---

	siparis, err := siparisSvc.GetOrder(ctx, siparisID)
	require.NoError(t, err, "oluşan sipariş sipariş modülünden okunabilmeli")
	assert.Equal(t, ordermodels.OrderPending, siparis.Status)
	assert.Equal(t, sepetID, siparis.CartID, "sipariş doğduğu sepeti belgelemeli")
	assert.Equal(t, musteriID, siparis.CustomerID)
	assert.Equal(t, vitrinAraToplam, siparis.Subtotal,
		"siparişin ara toplamı sepetinkiyle AYNI olmalı")
	assert.Equal(t, vitrinVergi, siparis.TaxTotal, "siparişin vergisi sepetinkiyle AYNI olmalı")
	assert.Equal(t, vitrinToplam, siparis.Total,
		"siparişin genel toplamı sepetinkiyle ve tahsil edilen tutarla aynı olmalı")
	assert.Equal(t, eposta, siparis.Email,
		"iletişim adresi SEPETTEN gelmeli; tamamlama gövdesi e-posta taşımıyor ve "+
			"taşıyamaz — adresin tek kaynağı sepettir")

	require.Len(t, siparis.Items, 1, "sepetin tek satırı siparişe tek satır olarak geçmeli")
	assert.Equal(t, vitrinBirimFiyat, siparis.Items[0].UnitPrice,
		"siparişteki birim fiyat da katalogdan gelen fiyat olmalı; istemcinin "+
			"gönderebileceği bir alan hiçbir adımda yoktu")
	assert.Equal(t, varyantBaslik, siparis.Items[0].Title,
		"satır başlığı katalogdan kopyalanmalı")

	kapali, err := sepetSvc.GetCart(ctx, sepetID)
	require.NoError(t, err)
	assert.True(t, kapali.Completed(),
		"sepet kapatılmalı; kapanmazsa aynı sepet ikinci bir siparişe kaynak olurdu")

	assert.Equal(t, vitrinKalanStok, satilabilirAdet(ctx, t, stokKalemID),
		"tahsilattan sonra stok fiziksel olarak düşmeli")

	olay := olayDefteri.bekle(t, siparisID)
	assert.Equal(t, siparisID, olay.Data["order_id"],
		"order.placed olayı yayımlanmalı; vitrinden verilen sipariş de bildirim üretmeli")
}

// TestVitrinIstemciFiyatiniReddeder vitrinin fiyat ve başlık KABUL ETMEDİĞİNİ
// gerçek uçta kanıtlar.
//
// İddia iki katmanlıdır: istek reddedilmeli VE sepete hiçbir satır
// yazılmamalı. Yalnızca status koduna bakmak yetmezdi — alan sessizce yok
// sayılıp satır yine eklenseydi, istemci gönderdiğini sanır ve sunucu başka
// bir fiyat yazardı.
func TestVitrinIstemciFiyatiniReddeder(t *testing.T) {
	ctx := t.Context()

	musteriID, eposta := yeniMusteri(ctx, t)
	varyantID, _ := yeniStokluVaryant(ctx, t, "E2E Fiyat Yetkisi Ürünü", map[string]int64{
		vergiliParaBirimi: vitrinBirimFiyat,
	}, vitrinBaslangicStok)

	sepetID := vitrinSepetiAc(t, musteriID, eposta)

	for ad, govde := range map[string]string{
		"bir kuruşluk fiyat": fmt.Sprintf(`{"variant_id":%q,"quantity":1,"unit_price":1}`, varyantID),
		"sıfır fiyat":        fmt.Sprintf(`{"variant_id":%q,"quantity":1,"unit_price":0}`, varyantID),
		"uydurma başlık":     fmt.Sprintf(`{"variant_id":%q,"quantity":1,"title":"Bedava"}`, varyantID),
	} {
		t.Run(ad, func(t *testing.T) {
			kayit := vitrinIstegi(t, http.MethodPost,
				"/store/v1/carts/"+sepetID+"/line-items", govde)

			assert.Equal(t, http.StatusUnprocessableEntity, kayit.Code,
				"vitrin fiyat/başlık kabul etmemeli; gövde: %s", kayit.Body.String())
		})
	}

	detay, err := sepetSvc.GetCart(ctx, sepetID)
	require.NoError(t, err)
	assert.Empty(t, detay.Items,
		"reddedilen isteklerin hiçbiri sepete satır yazmamalı")
}

// TestVitrinIstemciParaBiriminiReddeder sepet AÇMA ucunun para birimi kabul
// etmediğini gerçek uçta kanıtlar.
//
// Sınıf [TestVitrinIstemciFiyatiniReddeder] ile aynıdır: istemcinin
// belirlediği bir değer sunucunun fiyat kararına giriyordu. İstemci tutar
// uyduramıyordu ama HANGİ FİYAT LİSTESİNİN uygulanacağını seçebiliyordu — ve
// ayrışma reddedilmiyordu: sepet, bölgesi TRY olsa bile istemcinin dediği para
// biriminde açılıyor, satır da o listeden fiyatlanıyordu.
//
// İddia iki katmanlıdır: istek reddedilmeli VE hiçbir sepet yazılmamalı. Alan
// sessizce yok sayılsaydı istemci gönderdiğini sanır, sunucu başka bir para
// birimi yazardı.
func TestVitrinIstemciParaBiriminiReddeder(t *testing.T) {
	ctx := t.Context()

	oncekiSayi := sepetSayisi(ctx, t)

	// Gövdedeki para birimi bölgeninkinden BAŞKA: eskiden tam da bu istek,
	// TRY bölgesinde EUR fiyat listesiyle bir sepet açıyordu.
	red := vitrinIstegi(t, http.MethodPost, "/store/v1/carts", fmt.Sprintf(
		`{"region_id":%q,"currency_code":%q}`, vergiliBolgeID, vergisizParaBirimi))

	assert.Equal(t, http.StatusUnprocessableEntity, red.Code,
		"vitrin para birimi kabul etmemeli; gövde: %s", red.Body.String())
	assert.Equal(t, oncekiSayi, sepetSayisi(ctx, t),
		"reddedilen istek sepet YAZMAMALI; yazsaydı istemci gönderdiği para "+
			"biriminin uygulandığını sanırdı")
}

// TestVitrinParaBirimiBolgedenTuretilir sepetin para biriminin BÖLGEDEN
// geldiğini ve fiyatın gerçekten o para biriminin listesinden seçildiğini
// kanıtlar.
//
// İki bölge FARKLI para birimi taşır ve tek bir varyant ikisinde de
// fiyatlıdır. Yalnızca sepetin currency_code alanına bakmak yetmezdi: alan
// doğru yazılıp fiyat başka bir listeden okunsaydı iddia yine geçerdi. Asıl
// kanıt, aynı varyantın iki sepette FARKLI birim fiyat almasıdır — para birimi
// sepetin bir etiketi değil, fiyatın SEÇİCİSİDİR.
func TestVitrinParaBirimiBolgedenTuretilir(t *testing.T) {
	ctx := t.Context()

	// Tutarlar bilinçli olarak birbirine yakın DEĞİLDİR: bir kayma olsaydı
	// hangi listeden okunduğu tek bir sayıdan anlaşılsın.
	const (
		vergiliBolgeFiyat  int64 = 30_000
		vergisizBolgeFiyat int64 = 1_100
	)

	musteriID, eposta := yeniMusteri(ctx, t)
	varyantID := yeniVaryant(ctx, t, "E2E Para Birimi Ürünü", map[string]int64{
		vergiliParaBirimi:  vergiliBolgeFiyat,
		vergisizParaBirimi: vergisizBolgeFiyat,
	})

	for _, senaryo := range []struct {
		ad         string
		bolgeID    string
		paraBirimi string
		fiyat      int64
	}{
		{"vergili bölge", vergiliBolgeID, vergiliParaBirimi, vergiliBolgeFiyat},
		{"vergisiz bölge", vergisizBolgeID, vergisizParaBirimi, vergisizBolgeFiyat},
	} {
		t.Run(senaryo.ad, func(t *testing.T) {
			sepetID := vitrinBolgeSepetiAc(t, senaryo.bolgeID, musteriID, eposta)

			oku := vitrinIstegi(t, http.MethodGet, "/store/v1/carts/"+sepetID, "")
			require.Equal(t, http.StatusOK, oku.Code, "gövde: %s", oku.Body.String())
			assert.Equal(t, senaryo.paraBirimi, vitrinVeri(t, oku)["currency_code"],
				"sepetin para birimi BÖLGENİNKİ olmalı; istemci hiçbir kod göndermedi")

			ekle := vitrinIstegi(t, http.MethodPost, "/store/v1/carts/"+sepetID+"/line-items",
				fmt.Sprintf(`{"variant_id":%q,"quantity":1}`, varyantID))
			require.Equal(t, http.StatusCreated, ekle.Code, "gövde: %s", ekle.Body.String())

			assert.InDelta(t, float64(senaryo.fiyat), vitrinVeri(t, ekle)["unit_price"], 0,
				"birim fiyat sepetin para birimindeki listeden seçilmeli; yanlış "+
					"liste okunsaydı müşteri başka bir ülkenin fiyatını öderdi")
		})
	}
}

// TestVitrinOnaylanmayanToplamdaSiparisVermez müşterinin gördüğü toplam ile
// sunucunun hesapladığı toplam ayrıştığında HİÇBİR yan etki olmadığını
// kanıtlar.
//
// Bu, ödeme adımının en pahalı hatasına karşı korumadır: hesap tamamlama
// akışının başında YENİLENİR, yani katalogda değişen bir fiyat müşterinin
// onayladığından farklı bir tutarın çekilmesine yol açabilirdi. Kontrol
// saga'nın ilk adımından ÖNCE koştuğu için stok da ayrılmaz, sipariş de
// açılmaz — ve sepet hâlâ tamamlanabilir durumdadır.
func TestVitrinOnaylanmayanToplamdaSiparisVermez(t *testing.T) {
	ctx := t.Context()

	musteriID, eposta := yeniMusteri(ctx, t)
	varyantID, stokKalemID := yeniStokluVaryant(ctx, t, "E2E Onaylanan Toplam Ürünü",
		map[string]int64{vergiliParaBirimi: vitrinBirimFiyat}, vitrinBaslangicStok)

	sepetID := vitrinSepetiAc(t, musteriID, eposta)
	ekle := vitrinIstegi(t, http.MethodPost, "/store/v1/carts/"+sepetID+"/line-items",
		fmt.Sprintf(`{"variant_id":%q,"quantity":%d}`, varyantID, vitrinAdet))
	require.Equal(t, http.StatusCreated, ekle.Code, "gövde: %s", ekle.Body.String())

	// Onaylanan toplam bir kuruş eksik: müşteri BAŞKA bir tutar görmüş demektir.
	catisma := vitrinIstegi(t, http.MethodPost, "/store/v1/carts/"+sepetID+"/complete",
		vitrinTamamlamaGovdesi(t, vitrinToplam-1))
	require.Equal(t, http.StatusConflict, catisma.Code,
		"ayrışan toplam 409 olmalı; 500 görseydi istemci yeniden denerdi, oysa "+
			"yapması gereken müşteriye yeni tutarı onaylatmaktır. gövde: %s",
		catisma.Body.String())

	assert.Equal(t, vitrinBaslangicStok, satilabilirAdet(ctx, t, stokKalemID),
		"reddedilen tamamlama stok AYIRMAMALI")

	acik, err := sepetSvc.GetCart(ctx, sepetID)
	require.NoError(t, err)
	assert.False(t, acik.Completed(), "reddedilen tamamlama sepeti kapatmamalı")

	// Onaylanan toplam DOĞRU verildiğinde aynı sepet tamamlanabilmeli: reddedilen
	// deneme akışın idempotency anahtarını YAKMAMALI.
	tamam := vitrinIstegi(t, http.MethodPost, "/store/v1/carts/"+sepetID+"/complete",
		vitrinTamamlamaGovdesi(t, vitrinToplam))
	require.Equal(t, http.StatusOK, tamam.Code, "gövde: %s", tamam.Body.String())
	assert.NotEmpty(t, vitrinVeri(t, tamam)["order_id"])
}

// TestVitrinOnaylananToplamZorunlu korumanın istemci tarafından
// KAPATILAMADIĞINI doğrular.
//
// Alan opsiyonel olsaydı onu göndermeyi unutan her istemci korumayı sessizce
// devre dışı bırakırdı; bu deponun tekrar eden hata sınıfı tam olarak budur —
// kural tanımlıdır, uygulandığı yer yoktur.
func TestVitrinOnaylananToplamZorunlu(t *testing.T) {
	ctx := t.Context()

	musteriID, eposta := yeniMusteri(ctx, t)
	varyantID, _ := yeniStokluVaryant(ctx, t, "E2E Zorunlu Onay Ürünü",
		map[string]int64{vergiliParaBirimi: vitrinBirimFiyat}, vitrinBaslangicStok)

	sepetID := vitrinSepetiAc(t, musteriID, eposta)
	ekle := vitrinIstegi(t, http.MethodPost, "/store/v1/carts/"+sepetID+"/line-items",
		fmt.Sprintf(`{"variant_id":%q,"quantity":%d}`, varyantID, vitrinAdet))
	require.Equal(t, http.StatusCreated, ekle.Code, "gövde: %s", ekle.Body.String())

	eksik := vitrinIstegi(t, http.MethodPost, "/store/v1/carts/"+sepetID+"/complete",
		fmt.Sprintf(`{"payment_provider_id":%q}`, manual.ID))
	assert.Equal(t, http.StatusUnprocessableEntity, eksik.Code,
		"onaylanan toplam bildirilmeden sipariş verilememeli; gövde: %s", eksik.Body.String())

	acik, err := sepetSvc.GetCart(ctx, sepetID)
	require.NoError(t, err)
	assert.False(t, acik.Completed())
}

// TestVitrinB2BLimitReddiSebebiniBildirir reddin SEBEBİNİN vitrine ulaştığını
// kanıtlar.
//
// # Neden status kodu yetmiyor
//
// Harcama limitini aşan alışveriş zaten 409 alıyordu ve para da çekilmiyordu
// (bkz. b2b_test.go). Eksik olan tek şey, istemcinin bunu OKUYABİLMESİYDİ:
// gövdenin kodu, saga motorunun kendi sabitiyle ("workflow_step_failed")
// doluyordu ve "spending_limit" yanıtın hiçbir yerinde geçmiyordu; sebep
// yalnızca sunucu logundaydı.
//
// Fark davranışsaldır, kozmetik değil. 409 tam olarak TEKRARIN ÇÖZMEDİĞİ
// sınıftır: ayırt edemeyen bir vitrin ya kullanıcıya "geçici bir hata, tekrar
// deneyin" der ve limitini yükseltmesi gereken çalışanı boş yere döndürür, ya
// da her 409'u kalıcı sayıp gerçekten geçici olan çakışmaları da yutar. Bu,
// deponun tekrar eden hata sınıfının bir türevidir — kural uygulanıyor, hata
// kodu üretiliyor, ama tüketiciye ulaşmıyor.
//
// Zincir tümüyle HTTP'dir ve akışa hiç dokunulmaz: kodun gövdeye kadar geldiği
// ancak üretimdeki taşıma katmanından geçerek görülebilir.
func TestVitrinB2BLimitReddiSebebiniBildirir(t *testing.T) {
	ctx := t.Context()

	musteriID, eposta := yeniMusteri(ctx, t)
	varyantID, stokKalemID := yeniStokluVaryant(ctx, t, "E2E Vitrin B2B Limiti",
		map[string]int64{vergiliParaBirimi: vitrinBirimFiyat}, vitrinBaslangicStok)

	// Limit sepetin genel toplamının ALTINDA: 1_000 < 76_800.
	limit := int64(1_000)
	b2bCalisan(ctx, t, musteriID, &limit, b2bmodels.ResetNever)

	sepetID := vitrinSepetiAc(t, musteriID, eposta)
	ekle := vitrinIstegi(t, http.MethodPost, "/store/v1/carts/"+sepetID+"/line-items",
		fmt.Sprintf(`{"variant_id":%q,"quantity":%d}`, varyantID, vitrinAdet))
	require.Equal(t, http.StatusCreated, ekle.Code, "gövde: %s", ekle.Body.String())

	red := vitrinIstegi(t, http.MethodPost, "/store/v1/carts/"+sepetID+"/complete",
		vitrinTamamlamaGovdesi(t, vitrinToplam))

	require.Equal(t, http.StatusConflict, red.Code,
		"limit aşımı çakışmadır: istek biçimsel olarak geçerlidir, reddin sebebi "+
			"sistemin O ANDAKİ durumudur. gövde: %s", red.Body.String())
	assert.Equal(t, ordersvc.CodeSpendingLimitExceeded, hataKodu(t, red),
		"gövdenin kodu reddin SEBEBİNİ adlandırmalı; motorun kendi sabiti "+
			"görünüyorsa istemci limit aşımını geçici bir çakışmadan ayıramaz. "+
			"gövde: %s", red.Body.String())

	// Sebebin bildirilmesi, reddin bedelsiz olduğu iddiasını zayıflatmamalı.
	assert.Equal(t, vitrinBaslangicStok, satilabilirAdet(ctx, t, stokKalemID),
		"reddedilen alışveriş stoğu TUTMAMALI")

	acik, err := sepetSvc.GetCart(ctx, sepetID)
	require.NoError(t, err, "reddedilen alışverişin sepeti hâlâ okunabilmeli")
	assert.False(t, acik.Completed(),
		"reddedilen alışverişin sepeti KAPANMAMALI; kapansaydı müşteri limiti "+
			"düzeldiğinde sepetini yeniden kurmak zorunda kalırdı")
}

// vitrinSepetiAc mağaza ucundan VERGİLİ bölgede bir sepet açar ve kimliğini
// döner.
func vitrinSepetiAc(t *testing.T, musteriID, eposta string) string {
	t.Helper()

	return vitrinBolgeSepetiAc(t, vergiliBolgeID, musteriID, eposta)
}

// vitrinBolgeSepetiAc mağaza ucundan VERİLEN bölgede bir sepet açar ve
// kimliğini döner.
//
// Gövdede para birimi YOKTUR ve olamaz: sunucu onu bölgeden türetir. Bölgenin
// parametre olması tam da bunu görünür kılmak içindir — sepetin para birimini
// değiştirmenin tek yolu BAŞKA bir bölge seçmektir.
func vitrinBolgeSepetiAc(t *testing.T, bolgeID, musteriID, eposta string) string {
	t.Helper()

	kayit := vitrinIstegi(t, http.MethodPost, "/store/v1/carts", fmt.Sprintf(
		`{"region_id":%q,"customer_id":%q,"email":%q}`, bolgeID, musteriID, eposta))
	require.Equal(t, http.StatusCreated, kayit.Code, "sepet açılamadı; gövde: %s", kayit.Body.String())

	return sepetKimliginiOku(t, kayit)
}

// vitrinTamamlamaGovdesi tamamlama isteğinin gövdesini üretir.
//
// Ödeme davranışı manuel sağlayıcının oturum verisinden gelir
// (bkz. [odemeDavranisi]); saga'nın kendisinde hiçbir test kancası yoktur.
// Gövdede lokasyon YOKTUR ve olamaz: hangi depodan çıkılacağı kargo kararıdır
// ve akış onu satır başına kendisi verir.
func vitrinTamamlamaGovdesi(t *testing.T, onaylananToplam int64) string {
	t.Helper()

	govde, err := json.Marshal(map[string]any{
		"payment_provider_id": manual.ID,
		"payment_data":        odemeDavranisi(t, manual.OutcomeAuthorize),
		"expected_total":      onaylananToplam,
	})
	require.NoError(t, err, "tamamlama gövdesi kodlanamadı")

	return string(govde)
}

// vitrinIstegi publishable anahtarla bir mağaza isteği yapar.
//
// Anahtar ÜRETİMDEKİ koruma yığınından geçer: istek, korumasız bir router'a
// değil, cmd/server'daki sırayla kurulmuş olana gider (bkz. zeminiKur).
func vitrinIstegi(t *testing.T, metod, yol, govde string) *httptest.ResponseRecorder {
	t.Helper()

	var okuyucu *bytes.Reader
	if govde == "" {
		okuyucu = bytes.NewReader(nil)
	} else {
		okuyucu = bytes.NewReader([]byte(govde))
	}

	istek := httptest.NewRequest(metod, yol, okuyucu)
	istek.Header.Set("Content-Type", "application/json")
	istek.Header.Set(corehttp.PublishableKeyHeader, publishableAnahtar)

	kayit := httptest.NewRecorder()
	testRouter.ServeHTTP(kayit, istek)

	return kayit
}

// vitrinVeri yanıt zarfının "data" alanını nesne olarak döner.
func vitrinVeri(t *testing.T, kayit *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var zarf struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(kayit.Body.Bytes(), &zarf),
		"yanıt çözülemedi; gövde: %s", kayit.Body.String())
	require.NotNil(t, zarf.Data, "yanıt tekil zarf taşımalı; gövde: %s", kayit.Body.String())

	return zarf.Data
}
