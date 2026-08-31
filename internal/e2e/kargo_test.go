//go:build integration

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	fulfillmentmanual "github.com/bdrtr/gobit/internal/modules/fulfillment/manual"
	fulfillmentmodels "github.com/bdrtr/gobit/internal/modules/fulfillment/models"
	fulfillmentsvc "github.com/bdrtr/gobit/internal/modules/fulfillment/service"
	paymentmanual "github.com/bdrtr/gobit/internal/modules/payment/manual"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
)

// Bu dosya planın Faz 7 DoD'sinin KARGO ayağını kanıtlar: "siparişe fulfillment
// oluşturulabiliyor".
//
// İki senaryo vardır ve ikisi de fulfillment modülünün AYRI bir yüzeyinden
// geçer:
//
//   - Gönderi yaşam döngüsü, modüller arası yüzeyden ("fulfillment.interop").
//     Sipariş saga'sı kargo adımını bugün yürütmüyor, yani bu yüzeyin üretimde
//     hiçbir tüketicisi yok; imzalarını sabitleyen tek yer [kargoYuzeyi] dar
//     arayüzü ve bu dosyadır.
//   - Vitrin listelemesi, HTTP mağaza ucundan (/store/v1/shipping-options).
//     admin_only bir seçeneğin müşteriye görünmemesi bir servis kararı değil,
//     o ucun SABİTLEDİĞİ bir güven kararıdır (handler bayrağı sorgudan
//     okumaz, false yazar) ve ancak uçtan geçilerek kanıtlanabilir.
//
// # Sipariş referansı bir foreign key DEĞİLDİR
//
// fulfillment hiçbir modülü import etmez ve bir gönderinin hangi siparişe ait
// olduğunu DOĞRULAMAZ (Prensip 2.2); reference serbest bir metindir. Tam da bu
// yüzden bağın gerçekten kurulduğu ancak uçtan uca sınanabilir: modülün kendi
// testleri "verilen metin geri döndü mü" diye sorabilir, "geri dönen metin
// GERÇEKTEN oluşan siparişin kimliği mi" diye soramaz.

// Kargo senaryosunun ELLE hesaplanmış tutarları.
//
// Bölge %20 vergilidir ve sepete kargo YÖNTEMİ seçilmemiştir:
//
//	20_000 × 1 = 20_000 ara toplam
//	20_000 × %20 = 4_000 vergi
//	20_000 - 0 + 4_000 + 0 = 24_000 genel toplam
//
// Kargo seçeneğinin ücreti ([kargoSecenekUcreti]) sepete GİRMEZ: seçeneği
// listelemek onu sepete eklemek değildir ve sepetin kargo toplamı yalnızca
// SEÇİLMİŞ yöntemlerden oluşur.
const (
	kargoBirimFiyat int64 = 20_000
	kargoAdet       int64 = 1
	kargoAraToplam  int64 = 20_000
	kargoVergi      int64 = 4_000
	kargoToplam     int64 = 24_000
	kargoStok       int64 = 5

	// kargoSecenekUcreti vitrine çıkan normal seçeneğin sabit ücretidir.
	kargoSecenekUcreti int64 = 2_500
	// kargoYonetimUcreti YALNIZCA yönetime açık seçeneğin ücretidir.
	//
	// Normal seçenekten UCUZ olması bilinçlidir: liste ücrete göre artan
	// sıralıdır, yani bu seçenek vitrine sızsaydı BİRİNCİ sırada görünürdü.
	// Pahalı olsaydı yokluğu "sıralama/sayfalama" ile de açıklanabilirdi.
	kargoYonetimUcreti int64 = 1_500
)

// kargoSecenekIstegi [kargoYuzeyi.ListOptionsJSON] isteğinin JSON şemasıdır.
//
// Alan adları fulfillment'ın interop şemasıyla BİREBİR aynı olmak
// ZORUNDADIR: karşı taraf bilinmeyen alanları REDDEDER ve iki paket birbirini
// import edemediği için derleyici uyumu göremez (ADR 0006'nın kabul edilen
// bedeli). Şemanın tek yerde doğrulanabildiği yer budur.
type kargoSecenekIstegi struct {
	RegionID           string            `json:"region_id"`
	CurrencyCode       string            `json:"currency_code"`
	CountryCode        string            `json:"country_code"`
	ShippingProfileIDs []string          `json:"shipping_profile_ids"`
	Subtotal           int64             `json:"subtotal"`
	ItemCount          int64             `json:"item_count"`
	TotalWeight        int64             `json:"total_weight"`
	Attributes         map[string]string `json:"attributes"`
	IncludeAdminOnly   bool              `json:"include_admin_only"`
	IsReturn           bool              `json:"is_return"`
}

// kargoSecenekYaniti [kargoYuzeyi.ListOptionsJSON] yanıtının JSON şemasıdır.
type kargoSecenekYaniti struct {
	Options []kargoSecenek `json:"options"`
}

// kargoSecenek fiyatlanmış tek bir kargo seçeneğinin JSON şemasıdır.
type kargoSecenek struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Amount            int64  `json:"amount"`
	CurrencyCode      string `json:"currency_code"`
	PriceType         string `json:"price_type"`
	ProviderID        string `json:"provider_id"`
	ShippingProfileID string `json:"shipping_profile_id"`
	IsReturn          bool   `json:"is_return"`
	AdminOnly         bool   `json:"admin_only"`
}

// TestSiparisIcinGonderiOlusturulur Faz 7 DoD'sinin kargo ayağını uçtan uca
// koşturur.
//
// Zincir: sepet -> sipariş (Faz 6 akışı) -> kargo profili + seçeneği -> uygun
// seçeneklerin listelenmesi -> gönderi -> iptal. Sipariş GERÇEKTİR: kimliği
// uydurulmuş bir referansla açılan gönderi, "sipariş referansını taşıyor"
// iddiasını sınamış olmazdı.
func TestSiparisIcinGonderiOlusturulur(t *testing.T) {
	ctx := t.Context()

	musteriID, eposta := yeniMusteri(ctx, t)
	varyantID, _ := yeniStokluVaryant(ctx, t, "E2E Kargolu Ürün", map[string]int64{
		vergiliParaBirimi: kargoBirimFiyat,
	}, kargoStok)

	sepetID, toplamlar := sepetHazirla(ctx, t, musteriID, varyantID, kargoAdet)
	toplamlariDogrula(t, toplamlar, beklenenToplam{
		araToplam: kargoAraToplam,
		indirim:   0,
		vergi:     kargoVergi,
		kargo:     0,
		toplam:    kargoToplam,
	}, "kargo senaryosunun sepeti hazırlandıktan sonra")

	siparisSonucu, err := siparisAkislari.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID:            sepetID,
		LocationID:        stokLokasyonID,
		PaymentProviderID: paymentmanual.ID,
		PaymentData:       odemeDavranisi(t, paymentmanual.OutcomeAuthorize),
		Email:             eposta,
		ExpectedTotal:     kargoToplam,
	})
	require.NoError(t, err, "mutlu yoldan sipariş oluşabilmeli")
	require.NotEmpty(t, siparisSonucu.OrderID, "sipariş kimliği dönmeli")

	// --- 1) sipariş için uygun kargo seçenekleri ---

	profilID := yeniKargoProfili(ctx, t, "E2E Gönderi Profili")
	secenekID := yeniKargoSecenegi(ctx, t, profilID, "E2E Standart Kargo", kargoSecenekUcreti, false)

	secenekler := kargoSecenekleriniListele(ctx, t, kargoSecenekIstegi{
		RegionID:     vergiliBolgeID,
		CurrencyCode: vergiliParaBirimi,
		CountryCode:  vergiliUlke,
		// Süzgeç PROFİLE göre daraltılır: aynı veritabanını paylaşan başka
		// senaryoların seçenekleri de aynı bölgede yaşar ve süzgeçsiz bir
		// listeleme, testin sonucunu çalışma sırasına bağlardı.
		ShippingProfileIDs: []string{profilID},
		Subtotal:           kargoAraToplam,
		ItemCount:          kargoAdet,
	})

	require.Len(t, secenekler, 1,
		"profile bağlı TEK seçenek dönmeli; başka bir sayı, süzgecin uygulanmadığını "+
			"ya da seçeneğin uygun sayılmadığını gösterir")
	require.Equal(t, secenekID, secenekler[0].ID, "dönen seçenek az önce kurulan olmalı")
	require.Equal(t, kargoSecenekUcreti, secenekler[0].Amount,
		"sabit ücretli seçeneğin ücreti kataloğa yazılan tutar olmalı")
	require.Equal(t, vergiliParaBirimi, secenekler[0].CurrencyCode,
		"ücret sepetin para biriminde dönmeli; başka bir para birimi, iki farklı "+
			"birimdeki tutarların toplanması demek olurdu")
	require.Equal(t, fulfillmentmanual.ID, secenekler[0].ProviderID,
		"seçeneği yürütecek sağlayıcı kutudan çıkan manuel sağlayıcı olmalı")
	require.False(t, secenekler[0].AdminOnly,
		"vitrine açık seçenek admin_only OLMAMALI")

	// --- 2) siparişe gönderi ---

	anahtar := "e2e-gonderi-" + siparisSonucu.OrderID
	gonderiID, err := kargoInterop.CreateFulfillment(ctx, siparisSonucu.OrderID, secenekID, anahtar)
	require.NoError(t, err, "siparişe gönderi açılabilmeli")
	require.NotEmpty(t, gonderiID, "gönderi kimliği dönmeli")

	gonderi, err := kargoSvc.GetFulfillment(ctx, gonderiID)
	require.NoError(t, err, "gönderi fulfillment modülünden okunabilmeli")
	require.Equal(t, siparisSonucu.OrderID, gonderi.Reference,
		"gönderi SİPARİŞİN kimliğini referans olarak taşımalı. fulfillment bu alanı "+
			"doğrulamaz (foreign key değildir); yanlış ya da boş bir referans hiçbir "+
			"kısıta takılmaz, yalnızca mutabakatı imkânsız kılar — kargo etiketi hangi "+
			"siparişe ait olduğu bilinmeden basılmış olur")
	require.Equal(t, secenekID, gonderi.ShippingOptionID,
		"gönderi hangi kargo seçeneğiyle açıldığını belgelemeli")
	require.Equal(t, fulfillmentmanual.ID, gonderi.ProviderID,
		"gönderi seçeneğin sağlayıcısıyla açılmalı")
	require.Equal(t, fulfillmentmodels.StatusPending, gonderi.Status,
		"yeni gönderi 'pending' çıkmalı: kayıt açılmıştır ama kargo firması henüz "+
			"teslim almamıştır")
	require.NotEmpty(t, gonderi.ExternalID,
		"sağlayıcının kendi gönderi kimliği yazılmış olmalı. Belirsiz bir sağlayıcı "+
			"hatasından sonra iki sistemi eşleştirebilen TEK alan budur")

	durum, err := kargoInterop.FulfillmentStatus(ctx, gonderiID)
	require.NoError(t, err, "gönderi durumu yüzeyden okunabilmeli")
	require.Equal(t, fulfillmentmodels.StatusPending.String(), durum,
		"yüzeyin bildirdiği durum kaydınkiyle aynı olmalı")

	// --- 3) gönderi oluşturma İDEMPOTENT mi ---

	tekrarID, err := kargoInterop.CreateFulfillment(ctx, siparisSonucu.OrderID, secenekID, anahtar)
	require.NoError(t, err, "aynı anahtarla ikinci çağrı hata VERMEMELİ")
	require.Equal(t, gonderiID, tekrarID,
		"aynı idempotency anahtarı MEVCUT gönderiyi dönmeli. Yeni bir kimlik dönmesi, "+
			"yeniden denenen bir saga adımının İKİNCİ BİR KARGO ETİKETİ bastırdığı "+
			"anlamına gelirdi")

	referans := siparisSonucu.OrderID
	gonderiler, adet, err := kargoSvc.ListFulfillments(ctx, fulfillmentsvc.ListFulfillmentsInput{
		Reference: &referans,
	})
	require.NoError(t, err, "siparişin gönderileri listelenebilmeli")
	require.Equal(t, int64(1), adet,
		"siparişin defterinde TEK gönderi olmalı; ikinci bir satır, idempotency'nin "+
			"yalnızca dönen değerde sağlandığını ve deftere fazladan yazıldığını gösterir")
	require.Len(t, gonderiler, 1, "listelenen gönderi sayısı sayaçla tutarlı olmalı")
	require.Equal(t, gonderiID, gonderiler[0].ID, "listelenen gönderi açılan gönderi olmalı")

	// --- 4) iptal İDEMPOTENT mi (saga telafisi) ---

	require.NoError(t, kargoInterop.CancelFulfillment(ctx, gonderiID),
		"gönderi iptal edilebilmeli")

	iptalli, err := kargoSvc.GetFulfillment(ctx, gonderiID)
	require.NoError(t, err, "iptal edilmiş gönderi okunabilmeli")
	require.Equal(t, fulfillmentmodels.StatusCanceled, iptalli.Status,
		"gönderi 'canceled' olmalı; kayıt SİLİNMEZ, çünkü silinseydi ikinci bir iptal "+
			"çağrısı 'bilinmeyen kimlik' hatası verir ve telafi tekrar çalıştırılamazdı")
	require.NotNil(t, iptalli.CanceledAt, "iptal anı damgalanmalı")

	require.NoError(t, kargoInterop.CancelFulfillment(ctx, gonderiID),
		"İKİNCİ iptal çağrısı da hata VERMEMELİ. Telafinin tekrar çalıştırılabilir "+
			"olması bir tercih değil, saga'nın çalışma şartıdır: yeniden denenen bir "+
			"geri alma zinciri aynı adımı iki kez çağırır")

	ikinciOkuma, err := kargoSvc.GetFulfillment(ctx, gonderiID)
	require.NoError(t, err, "ikinci iptalden sonra gönderi yine okunabilmeli")
	require.Equal(t, fulfillmentmodels.StatusCanceled, ikinciOkuma.Status,
		"durum 'canceled' kalmalı")
	require.Equal(t, iptalli.CanceledAt, ikinciOkuma.CanceledAt,
		"iptal damgası DEĞİŞMEMELİ; değişmesi, ikinci çağrının kaydı yeniden yazdığını "+
			"ve dolayısıyla sağlayıcıya da ikinci kez gidildiğini gösterirdi")
	require.Equal(t, iptalli.UpdatedAt, ikinciOkuma.UpdatedAt,
		"kayıt ikinci çağrıda hiç GÜNCELLENMEMELİ. İdempotentlik 'aynı sonucu üret' "+
			"değil, 'ikinci kez hiçbir şey yapma' demektir; aksi hâlde her yeniden "+
			"deneme mutabakat defterine yeni bir hareket yazardı")

	err = kargoInterop.CancelFulfillment(ctx, fulfillmentmodels.NewFulfillmentID())
	require.Error(t, err, "BİLİNMEYEN bir kimliğin iptali hata vermeli")
	require.True(t, errors.IsNotFound(err),
		"hata NotFound olmalı, sessizce yutulmamalı. İdempotentlik 'her şeyi kabul et' "+
			"demek değildir: iki kez iptal edilen GERÇEK bir gönderi ile hiç var olmamış "+
			"bir kimlik farklı durumlardır ve ikincisi çağıran tarafta bir hatadır "+
			"(alınan: %v)", err)
}

// TestMagazaYuzeyiYonetimSeceneginiGizler admin_only bir kargo seçeneğinin
// vitrine ÇIKMADIĞINI doğrular.
//
// İki seçenek aynı profile ve aynı bölgeye kurulur; tek fark admin_only
// bayrağıdır. Yönetime özel olan bilinçli olarak DAHA UCUZDUR: liste ücrete
// göre artan sıralı olduğu için sızsaydı birinci sırada görünürdü, yani
// yokluğu sıralamayla açıklanamaz.
//
// Aynı bağlam iki yüzeyden sorgulanır. Mağaza ucu seçeneği HİÇ görmezken,
// yönetim bayrağını açan modüller arası yüzey ikisini de döner; ikinci sorgu
// olmasaydı seçeneğin vitrinde görünmemesi "gizlendi" değil "uygun bulunmadı"
// ya da "hiç oluşmadı" ile de açıklanabilirdi.
func TestMagazaYuzeyiYonetimSeceneginiGizler(t *testing.T) {
	ctx := t.Context()

	profilID := yeniKargoProfili(ctx, t, "E2E Vitrin Profili")
	vitrinID := yeniKargoSecenegi(ctx, t, profilID, "E2E Vitrin Kargosu", kargoSecenekUcreti, false)
	yonetimID := yeniKargoSecenegi(ctx, t, profilID, "E2E Yönetim Kargosu", kargoYonetimUcreti, true)

	sorgu := url.Values{}
	sorgu.Set("region_id", vergiliBolgeID)
	sorgu.Set("currency_code", vergiliParaBirimi)
	sorgu.Set("country_code", vergiliUlke)
	sorgu.Set("shipping_profile_id", profilID)
	sorgu.Set("subtotal", strconv.FormatInt(kargoAraToplam, 10))
	sorgu.Set("item_count", strconv.FormatInt(kargoAdet, 10))

	vitrin := magazaKargoSecenekleri(t, sorgu)

	require.Len(t, vitrin, 1,
		"mağaza ucundan TEK seçenek dönmeli: yalnızca vitrine açık olan")
	require.Equal(t, vitrinID, vitrin[0]["id"],
		"dönen seçenek admin_only OLMAYAN seçenek olmalı")
	require.Equal(t, float64(kargoSecenekUcreti), vitrin[0]["amount"],
		"vitrindeki ücret kataloğa yazılan tutar olmalı")

	for _, kayit := range vitrin {
		require.NotEqual(t, yonetimID, kayit["id"],
			"admin_only seçenek vitrinde GÖRÜNMEMELİ. Süzgeç SQL'dedir ve satır mağaza "+
				"yolunda hiç okunmaz; sızması, yönetime özel bir kargo anlaşmasının "+
				"fiyatıyla birlikte müşteriye açılması demek olurdu")
	}

	// Vitrin gösterimi kataloğun iç yapısını da SIZDIRMAMALI.
	require.NotContains(t, vitrin[0], "provider_id",
		"mağaza gösterimi sağlayıcı kimliğini taşımamalı; hangi kargo firmasıyla "+
			"çalışıldığı mağazanın operasyonel bilgisidir")
	require.NotContains(t, vitrin[0], "admin_only",
		"mağaza gösterimi admin_only alanını hiç taşımamalı; alan yalnızca yönetim "+
			"gösteriminde vardır ve iki DTO'nun ayrı olması sızıntıyı yapısal olarak "+
			"engeller")
	require.NotContains(t, vitrin[0], "shipping_profile_id",
		"mağaza gösterimi profil kimliğini taşımamalı; profil kataloğun iç yapısıdır")

	// --- seçenek GERÇEKTEN var ve uygun mu: yönetim bayrağıyla aynı bağlam ---

	hepsi := kargoSecenekleriniListele(ctx, t, kargoSecenekIstegi{
		RegionID:           vergiliBolgeID,
		CurrencyCode:       vergiliParaBirimi,
		CountryCode:        vergiliUlke,
		ShippingProfileIDs: []string{profilID},
		Subtotal:           kargoAraToplam,
		ItemCount:          kargoAdet,
		IncludeAdminOnly:   true,
	})

	require.Len(t, hepsi, 2,
		"yönetim bayrağı açıkken İKİ seçenek de dönmeli; dönmeseydi vitrindeki "+
			"yokluk gizlemeyle değil, seçeneğin hiç uygun olmamasıyla açıklanırdı")
	require.Equal(t, yonetimID, hepsi[0].ID,
		"liste ücrete göre ARTAN sıralı olmalı ve daha ucuz olan yönetim seçeneği "+
			"başta gelmeli; bu, seçeneğin vitrinde neden görünmediğinin sıralama ya da "+
			"kırpma olmadığını kesinleştirir")
	require.True(t, hepsi[0].AdminOnly, "ilk seçenek admin_only olmalı")
	require.Equal(t, vitrinID, hepsi[1].ID, "ikinci seçenek vitrine açık olan olmalı")
	require.False(t, hepsi[1].AdminOnly, "ikinci seçenek admin_only OLMAMALI")
}

// yeniKargoProfili test için bir kargo profili açar ve kimliğini döner.
//
// Ad benzersizleştirilir: fulfillment aynı adla ikinci bir profili reddeder ve
// testler tek bir veritabanını paylaşır.
func yeniKargoProfili(ctx context.Context, t *testing.T, ad string) string {
	t.Helper()

	profil, err := kargoSvc.CreateShippingProfile(ctx, fulfillmentsvc.CreateProfileInput{
		Name: fmt.Sprintf("%s %d", ad, fiksturSayaci.Add(1)),
	})
	require.NoError(t, err, "fikstür kargo profili oluşturulamadı")
	return profil.ID
}

// yeniKargoSecenegi verilen profile SABİT ücretli bir kargo seçeneği açar ve
// kimliğini döner.
//
// Seçenek kuralsızdır, yani koşulsuz uygundur: bu dosyanın sınadığı şey
// uygunluk kuralları değil, admin_only süzgeci ile gönderinin yaşam
// döngüsüdür. Kurallı bir seçenek, mağaza ucunda sepet olguları
// doğrulanamadığı için zaten hiç listelenmezdi ve iki ayrı sebep birbirine
// karışırdı.
func yeniKargoSecenegi(
	ctx context.Context,
	t *testing.T,
	profilID, ad string,
	ucret int64,
	yalnizcaYonetim bool,
) string {
	t.Helper()

	secenek, err := kargoSvc.CreateShippingOption(ctx, fulfillmentsvc.CreateOptionInput{
		Name:              fmt.Sprintf("%s %d", ad, fiksturSayaci.Add(1)),
		ProviderID:        fulfillmentmanual.ID,
		ShippingProfileID: profilID,
		Amount:            ucret,
		CurrencyCode:      vergiliParaBirimi,
		RegionID:          vergiliBolgeID,
		AdminOnly:         yalnizcaYonetim,
	})
	require.NoError(t, err, "fikstür kargo seçeneği oluşturulamadı")
	return secenek.ID
}

// kargoSecenekleriniListele uygun kargo seçeneklerini modüller arası yüzeyden
// listeler.
//
// İstek ve yanıt, iki pakette AYRI AYRI tanımlanmış JSON şemalarıyla taşınır
// ve karşı taraf bilinmeyen alanları REDDEDER; bir alan adı kayarsa çağrı
// burada açık bir hatayla düşer.
func kargoSecenekleriniListele(
	ctx context.Context,
	t *testing.T,
	istek kargoSecenekIstegi,
) []kargoSecenek {
	t.Helper()

	govde, err := json.Marshal(istek)
	require.NoError(t, err, "kargo seçeneği isteği kodlanamadı")

	ham, err := kargoInterop.ListOptionsJSON(ctx, govde)
	require.NoError(t, err, "kargo seçenekleri listelenemedi")

	var yanit kargoSecenekYaniti
	require.NoError(t, json.Unmarshal(ham, &yanit),
		"kargo seçeneği yanıtı çözülemedi; şema iki pakette ayrışmış olabilir")
	return yanit.Options
}

// magazaKargoSecenekleri /store/v1/shipping-options ucunu çağırır ve dönen
// kayıtları ÇÖZÜLMEMİŞ haritalar olarak döner.
//
// Kayıtların tipli bir yapıya değil haritaya çözülmesi bilinçlidir: mağaza
// gösteriminde hangi alanların BULUNMADIĞI da bir iddiadır ve tipli bir yapı,
// yanıtta duran fazladan bir alanı sessizce atardı.
func magazaKargoSecenekleri(t *testing.T, sorgu url.Values) []map[string]any {
	t.Helper()

	istek := httptest.NewRequest(http.MethodGet,
		"/store/v1/shipping-options?"+sorgu.Encode(), http.NoBody)
	// Mağaza yüzeyi Faz 8'den beri publishable anahtar ister; anahtarsız
	// istek daha router'a varmadan 401 olur (bkz. kimlik_test.go).
	istek.Header.Set(corehttp.PublishableKeyHeader, publishableAnahtar)
	kayit := httptest.NewRecorder()
	testRouter.ServeHTTP(kayit, istek)

	require.Equal(t, http.StatusOK, kayit.Code,
		"mağaza ucu 200 dönmeli; gövde: %s", kayit.Body.String())

	var zarf struct {
		Data  []map[string]any `json:"data"`
		Count int64            `json:"count"`
	}
	require.NoError(t, json.Unmarshal(kayit.Body.Bytes(), &zarf),
		"mağaza yanıtı çözülemedi; gövde: %s", kayit.Body.String())
	require.Equal(t, int64(len(zarf.Data)), zarf.Count,
		"zarftaki sayaç, dönen kayıt sayısıyla tutarlı olmalı")

	return zarf.Data
}
