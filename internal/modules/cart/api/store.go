package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/cart/models"
	"github.com/bdrtr/gobit/internal/modules/cart/service"
)

// createCartRequest POST /store/v1/carts gövdesidir.
//
// # currency_code BURADA YOKTUR
//
// Alan bir zamanlar bu gövdedeydi ve SUNUCUNUN verisiydi — sınıfı, aynı
// ölçütle kaldırılan unit_price ile aynıdır ([addLineItemRequest]).
//
// Para birimi bölgenin verisidir: region şemasında bölge başına TEK bir
// sütundur (region.currency_code, currency tablosuna FK). Bir bölgenin iki
// para birimi olamaz, dolayısıyla sepetin para birimi bir SEÇİM değil bir
// TÜRETMEDİR ve bugün handler onu bölgeden türetir
// ([Handler.currencyForRegion]).
//
// Türetmenin bedeli kozmetik değildi. Para birimi FİYAT SEÇER: satır
// fiyatlandırma akışı birim fiyatı varyantın fiyat kümesinden "sepetin para
// biriminde" okur. Alan gövdedeyken istemci tutar uyduramıyordu ama HANGİ
// FİYAT LİSTESİNİN uygulanacağını seçebiliyordu — TRY bölgesinde açtığı sepete
// USD yazan bir istemci, operatörün USD listesindeki fiyatı ödüyordu. Üstelik
// ayrışma REDDEDİLMİYORDU: cart servisi region'ı tanımadığı için (ADR 0006)
// kodun yalnızca BİÇİMİNİ doğruluyor, bölgeninkiyle karşılaştırmıyordu.
//
// Gövde tanınmayan alanı REDDEDER ([decodeBody]), yani "currency_code" gönderen
// eski bir istemci 422 alır. Sessizce yok saymak seçilmedi: istemci
// gönderdiğini sanır, sunucu başka bir para birimi yazardı — ve fiyat listesi
// beklediğinden başkası olurdu.
//
// # YÖNETİM yüzeyinde aynı alan MEŞRUDUR
//
// Para biriminin gövdede yer alması her yerde kusur değildir; soru "bu değer
// çağıranın kendi verisi mi" sorusudur. POST /admin/v1/regions gövdesindeki
// currency_code bölgeyi TANIMLAR — operatör orada bir kopya değil ASLI yazar
// ve kopyalanacak bir kaynak yoktur. Burada ise aynı alan, sunucunun zaten
// bildiği bir değerin istemci tarafından tekrar edilmesiydi. Cart'ın kendi
// yönetim yüzeyinde bu soru hiç doğmaz: /admin/v1/carts YALNIZCA OKUR ve
// sepet açan bir yönetim ucu yoktur.
//
// # region_id hâlâ İSTEMCİDEN geliyor ve bu bir BORÇTUR
//
// Bölge vergi ORANINI seçer ve alan aynı sınıftadır. İki şey değişti:
// bölgenin gerçekten VAR OLDUĞU artık doğrulanır (para birimi ondan
// okunduğu için uydurma bir kimlik sepet açamaz) ve seçimin fiyat listesi
// üzerindeki etkisi kalktı. Doğru kapatma yeri yine bu handler değildir:
// türetmeyi zaten yapan bir akış vardır — internal/workflows/cart'taki
// create_cart ülke kodundan hem bölgeyi hem para birimini çözer — ve ucun
// gövdesi country_code'a indirilerek O AKIŞA devredilmelidir. Maliyeti,
// akışın modüller arası yüzeyine bugün bilinçli olarak bulunmayan bir metot
// eklemektir.
//
// Borç burada YAZILIDIR: kayda geçmemiş bir açık, kimsenin kapatmadığı
// açıktır.
type createCartRequest struct {
	// RegionID zorunludur; sepetin para birimi de ondan türetilir.
	RegionID string `json:"region_id"`
	// CustomerID boş bırakılırsa sepet misafirindir.
	//
	// Alan bir SAHİPLİK İDDİASIDIR ve bugün hiçbir kanıt istemez; sınırı
	// paket belgesindeki "Vitrin sepetlerinde sahiplik" bölümündedir.
	CustomerID string         `json:"customer_id"`
	Email      string         `json:"email"`
	Metadata   map[string]any `json:"metadata"`
}

// storeCreateCart yeni bir sepet oluşturur; para birimini SUNUCU belirler.
//
// Para birimi gövdeden değil BÖLGEDEN gelir ([Handler.currencyForRegion]) ve
// sıra bilinçlidir: türetme, sepet YAZILMADAN önce koşar. Sonraya bırakılsaydı
// bölgesi bilinmeyen bir sepet açılmış olur ve para birimi ancak ikinci bir
// yazmayla düzelebilirdi.
func (h *Handler) storeCreateCart(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body createCartRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	currency, err := h.currencyForRegion(ctx, body.RegionID)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	cart, err := h.svc.CreateCart(ctx, service.CreateCartInput{
		RegionID:     body.RegionID,
		CustomerID:   body.CustomerID,
		Email:        body.Email,
		CurrencyCode: currency,
		Metadata:     body.Metadata,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusCreated, singleEnvelope{Data: toCartDTO(cart)})
}

// storeGetCart sepeti çocuklarıyla döner.
func (h *Handler) storeGetCart(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	detail, err := h.svc.GetCart(ctx, cartID(r))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toCartDetailDTO(detail)})
}

// updateCartRequest POST /store/v1/carts/{id} gövdesidir.
type updateCartRequest struct {
	// Email işaretçidir: gövdede HİÇ gönderilmemiş e-posta ile boşaltılmak
	// istenen e-posta ayrı niyetlerdir. İkisi tek boş dizeye indirgenseydi,
	// yalnızca müşteri devretmek isteyen her istek sepetin e-postasını
	// sessizce silerdi.
	Email *string `json:"email"`
	// CustomerID misafir sepeti devralacak müşteridir; boş bırakılırsa sepetin
	// müşterisine dokunulmaz.
	CustomerID string `json:"customer_id"`
}

// storeUpdateCart sepetin e-postasını ve/veya müşterisini günceller.
//
// Ödeme adımında e-posta toplamak ve misafir sepeti giriş yapan müşteriye
// devretmek için vardır. Uç PATCH değil POST'tur: chi yönlendirmesi zaten
// gövdeye göre dallanmaz ve müşteri tarafındaki diğer yazmalar da POST'tur.
func (h *Handler) storeUpdateCart(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body updateCartRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	cart, err := h.svc.UpdateCart(ctx, cartID(r), service.UpdateCartInput{
		Email:      body.Email,
		CustomerID: body.CustomerID,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toCartDTO(cart)})
}

// storeDeleteCart sepeti yumuşak siler.
func (h *Handler) storeDeleteCart(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := h.svc.DeleteCart(ctx, cartID(r)); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// addLineItemRequest POST /store/v1/carts/{id}/line-items gövdesidir.
//
// # FİYAT VE BAŞLIK BURADA YOKTUR
//
// İkisi de bir zamanlar bu gövdedeydi ve ikisi de SUNUCUNUN bilgisidir:
//
//   - unit_price. Alanın godoc'u "opsiyoneldir; nihai fiyatı calculate_totals
//     workflow'u yazar" diyordu, ama o workflow hiçbir kurulumda kablolanmamıştı;
//     yani istemcinin gönderdiği tutar NİHAİ tutardı. Vitrinin kimliği
//     publishable anahtardır ve tarayıcıda durur, dolayısıyla bu, herkesin
//     erişebildiği bir "kendi fiyatını yaz" ucuydu. Fiyat artık
//     [LinePricing] üzerinden pricing modülünden gelir.
//   - title. Satırın adı KATALOĞUN verisidir; istemciden alınması, sepette ve
//     dolayısıyla siparişte, faturada ve kargo listesinde ürünün gerçek adıyla
//     ilgisi olmayan bir metnin görünmesi demekti. Başlık artık akış tarafından
//     Query katmanından okunur (bkz. akışın snapshot/catalog tarafı).
//
// Gövde tanınmayan alanı REDDEDER ([decodeBody]), yani eski bir istemci
// sessizce eski davranışa dönmez: "unit_price" gönderen istek 422 alır. Kırıcı
// olması bilinçlidir — sessiz kabul, kaldırılan arızayı geri getirirdi.
type addLineItemRequest struct {
	VariantID string `json:"variant_id"`
	// Quantity işaretçidir: gönderilmeyen adet ile sıfır adet birbirinden
	// ayrılsın diye. Sıfır adet zaten geçersizdir, ama iki durum FARKLI
	// mesajlar hak eder.
	Quantity *int64 `json:"quantity"`
	// Metadata satırın serbest ek verisidir (hediye notu, kişiselleştirme).
	//
	// Fiyattan farklı olarak bu, gerçekten İSTEMCİNİN bilgisidir ve hiçbir
	// hesaba girmez; bu yüzden gövdede kalır ve akışa olduğu gibi taşınır.
	Metadata map[string]any `json:"metadata"`
}

// storeAddLineItem sepete satır ekler; fiyatı SUNUCU belirler.
//
// Satırı bu handler değil [LinePricing] yazar: akış varyantın başlığını
// katalogdan, fiyatını pricing modülünden alır, satırı sepete ekler ve sepetin
// toplamlarını yeniden hesaplar. Fiyatlandırıcı çözülemiyorsa satır HİÇ
// eklenmez (bkz. [Handler.pricing]).
func (h *Handler) storeAddLineItem(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body addLineItemRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	if body.Quantity == nil {
		corehttp.WriteError(ctx, w, coreerrors.Invalid(codeInvalidRequest, "quantity zorunludur"))
		return
	}
	akis, err := h.pricing()
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	metadata, err := encodeMetadata(body.Metadata)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	id := cartID(r)
	lineID, err := akis.AddPricedLineItem(ctx, id, body.VariantID, *body.Quantity, metadata)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	item, err := h.lineItem(ctx, id, lineID)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusCreated, singleEnvelope{Data: toLineItemDTO(item)})
}

// updateLineItemRequest satır güncelleme gövdesidir.
type updateLineItemRequest struct {
	// Quantity işaretçidir; bkz. [addLineItemRequest].
	Quantity *int64 `json:"quantity"`
}

// storeUpdateLineItem satırın adedini yazar ve satırı YENİDEN FİYATLAR.
//
// Adedi yazan yol da akıştan geçer, çünkü adet fiyatı DEĞİŞTİREBİLİR: pricing
// birim fiyatı adet aralığına göre seçer (3 adet ile 5 adet farklı kademedir).
// Servise doğrudan yazılsaydı satır, yeni adetle ama ESKİ kademenin fiyatıyla
// kalır ve sepetin toplamı bayatlardı.
//
// # Sıfır adet satırı KALDIRIR ve 204 döner
//
// Sıfır adet eskiden 422'ydi; artık akışın niyet çevirisi geçerlidir (her
// sepet arayüzünde adet seçiciyi sıfıra indirmek "bunu kaldır" demektir, bkz.
// akıştaki UpdateLineItem). Uç bu durumda gövdesiz 204 döner: kaldırılmış bir
// satırın kaydını yanıtta sunmak, istemciye artık var olmayan bir kaynağı
// vermek olurdu.
func (h *Handler) storeUpdateLineItem(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body updateLineItemRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	if body.Quantity == nil {
		corehttp.WriteError(ctx, w, coreerrors.Invalid(codeInvalidRequest, "quantity zorunludur"))
		return
	}
	akis, err := h.pricing()
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	id, lineID := cartID(r), chi.URLParam(r, paramLineItemID)
	removed, err := akis.SetLineItemQuantity(ctx, id, lineID, *body.Quantity)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	if removed {
		corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
		return
	}

	item, err := h.lineItem(ctx, id, lineID)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toLineItemDTO(item)})
}

// lineItem yazılmış satırı sepetten OKUYUP döner.
//
// # Neden yeniden okunuyor
//
// Akış geriye yalnızca satırın kimliğini verir ve bu bilinçlidir: yüzeyi ilkel
// tiplerle sınırlıdır, sepetin zengin kaydını taşıyamaz. Ama okumanın asıl
// sebebi bu değil: satırın TUTARLARI (birim fiyat, ara toplam, indirim, vergi)
// satır yazıldıktan SONRA koşan hesap turunda yazılır. Akıştan dönen anlık
// değeri sunmak, müşteriye sepetteki tutardan farklı bir sayı göstermek
// olurdu — hem de tam olarak fiyatın kaynağını düzelttiğimiz uçta.
//
// Satır bulunamazsa hata Internal'dır: az önce yazılmış bir kaydın okunamaması
// istemcinin düzeltebileceği bir şey değildir.
func (h *Handler) lineItem(ctx context.Context, cartID, lineID string) (models.LineItem, error) {
	detail, err := h.svc.GetCart(ctx, cartID)
	if err != nil {
		return models.LineItem{}, err
	}
	for i := range detail.Items {
		if detail.Items[i].ID == lineID {
			return detail.Items[i], nil
		}
	}
	return models.LineItem{}, coreerrors.Internal(codeLineItemMissing,
		"satır yazıldı ama sepette bulunamadı: %s (%s)", lineID, cartID)
}

// encodeMetadata serbest ek veriyi akışın taşıdığı JSON'a çevirir; boş harita
// nil döner.
//
// Çevirinin gerekmesi, akış yüzeyinin yalnızca ilkel ve stdlib tipleri
// kullanabilmesindendir (ADR 0006): map[string]any bu paketin tipi değil ama
// akışın imzasında yer alması, sınırın iki ucunu aynı Go tipine bağlamak
// olurdu. Kodlama hatası errors.Invalid'dir — gövde istemciden gelir ve
// kodlanamayan tek durum, JSON'a çevrilemeyen bir değerdir.
func encodeMetadata(metadata map[string]any) (json.RawMessage, error) {
	if len(metadata) == 0 {
		return nil, nil
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		return nil, coreerrors.Wrap(err, coreerrors.KindInvalid, codeInvalidRequest,
			"metadata JSON'a çevrilemedi")
	}
	return raw, nil
}

// storeRemoveLineItem satırı sepetten kaldırır.
func (h *Handler) storeRemoveLineItem(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := h.svc.RemoveLineItem(ctx, cartID(r), chi.URLParam(r, paramLineItemID)); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// completeCartRequest POST /store/v1/carts/{id}/complete gövdesidir.
//
// # Hangi alan neden BURADA, hangisi neden DEĞİL
//
// Her alan bir yetki sorusudur: gövdeye konan şey, müşterinin belirleyebildiği
// şeydir.
//
//   - payment_provider_id VARDIR. Hangi sağlayıcıyla ödeneceği müşterinin
//     seçimidir ve sunucunun bir varsayılanı olamaz. Yetki sorunu doğurmaz:
//     tanınmayan bir ad tahsilat açmaz, akışı düşürür.
//   - payment_data VARDIR. Sağlayıcıya olduğu gibi iletilen serbest veridir
//     (kart tokenı, dönüş adresi); tanımı gereği istemcinin bilgisidir.
//   - expected_total VARDIR ve ZORUNLUDUR; gerekçe alanın godoc'undadır.
//   - email YOKTUR. Sepetin iletişim adresi zaten sepette durur ve buraya
//     ikinci bir kanal açmak, siparişin sepette görünenden BAŞKA bir adrese
//     bağlanmasına izin verirdi. Handler onu kendi servisinden okur — akışın
//     "sepet modülünün yüzeyi e-postayı yayımlamıyor" kısıtı bu uçta yoktur,
//     çünkü sepetin sahibi bu modüldür.
//   - location_id YOKTUR. Hangi depodan çıkılacağı bir kargo kararıdır ve
//     akış onu satır başına stok + kargo modüllerine sorarak verir; müşteriye
//     depo seçtirmek hem stok topolojisini sızdırır hem de siparişin nereden
//     çıkacağını ona bırakırdı.
type completeCartRequest struct {
	PaymentProviderID string `json:"payment_provider_id"`
	// PaymentData sağlayıcıya olduğu gibi iletilir; opsiyoneldir.
	PaymentData json.RawMessage `json:"payment_data"`
	// ExpectedTotal müşterinin ONAYLADIĞI genel toplamdır (minor unit);
	// ZORUNLUDUR.
	//
	// # Neden zorunlu
	//
	// Hesap, tamamlama akışının başında YENİLENİR: katalogdaki bir fiyat
	// değişikliği ya da süresi dolan bir promosyon, müşterinin gördüğü tutarla
	// çekilecek tutarı ayırabilir. Alan gönderilirse fark errors.Conflict
	// üretir ve HİÇBİR yan etki uygulanmaz — kontrol saga'nın ilk adımından
	// önce koşar.
	//
	// İşaretçidir ve eksikliği 422'dir: opsiyonel bırakılsaydı alanı unutan
	// her istemci, korumayı sessizce kapatırdı. Bu depoda tekrar eden hata
	// sınıfı tam olarak budur — kural tanımlıdır, uygulandığı yer yoktur.
	//
	// Sıfır "karşılaştırma yapma" demektir ve yalnızca gerçekten sıfır tutan
	// bir sepet için meşrudur. Bir güvenlik boşluğu DEĞİLDİR: karşılaştırmayı
	// atlayan istemci yine sunucunun hesapladığı tutarı öder, yalnızca
	// müşterisini fiyat değişiminden haberdar etme fırsatını kaybeder.
	ExpectedTotal *int64 `json:"expected_total"`
}

// completeCartFlowRequest tamamlama akışına gönderilen JSON'un şemasıdır.
//
// Alan adları akış tarafındaki şemayla BİREBİR aynı olmak zorundadır; bu modül
// internal/workflows'u import edemediği için (ADR 0006) uyumu derleyici
// denetlemez ve ancak entegrasyon testiyle kanıtlanır (bkz. internal/e2e).
type completeCartFlowRequest struct {
	CartID            string          `json:"cart_id"`
	PaymentProviderID string          `json:"payment_provider_id"`
	PaymentData       json.RawMessage `json:"payment_data,omitempty"`
	Email             string          `json:"email,omitempty"`
	ExpectedTotal     int64           `json:"expected_total"`
}

// completeCartFlowResult tamamlama akışından dönen JSON'un şemasıdır.
type completeCartFlowResult struct {
	OrderID      string `json:"order_id"`
	CartID       string `json:"cart_id"`
	CurrencyCode string `json:"currency_code"`
	Amount       int64  `json:"amount"`
}

// completeCartDTO tamamlanan sepetin dış gösterimidir.
//
// Yanıt siparişin KİMLİĞİNİ ve tahsil edilen tutarı taşır, başka bir şey
// taşımaz: ödeme oturumu ve rezervasyon kimlikleri iç yapıdır, uyarılar ise
// operatörün işidir (akış onları loglar). Siparişin ayrıntısı
// GET /store/v1/orders/{id} ile okunur.
type completeCartDTO struct {
	OrderID      string `json:"order_id"`
	CartID       string `json:"cart_id"`
	CurrencyCode string `json:"currency_code"`
	Total        int64  `json:"total"`
}

// storeCompleteCart sepeti siparişe çevirir.
//
// # Neden bu uç BU modülde
//
// Akışların HTTP sahibi modüldür (ADR 0001'in kalıbı): sepetin uçlarının
// sahibi cart modülüdür, dolayısıyla sepeti kapatan uç da buradadır. Bileşim
// kökü yalnızca akışı KURAR ve container'a bırakır; handler kodu oraya
// girmez. Modül somut akışı tanımaz, [CartCompletion] arayüzüyle konuşur.
//
// # Neden /complete
//
// Yol sepetin altındadır ve bir FİİL taşır: uç bir kaynak yaratmaz, sepetin
// durumunu değiştirir (ve yan ürünü bir siparişdir). Alternatif
// POST /store/v1/orders olurdu ama sipariş oluşturma order modülünün
// yüzeyidir ve o modül sepeti bilmez; ucun oraya konması, siparişi sepetten
// üreten bilgiyi hiç sahibi olmayan bir yere taşırdı.
//
// # Neden 200, 201 değil
//
// Yaratılan kaynak (sipariş) BU yolun kaynağı değildir ve bu uç ona bir adres
// veremez; 201, "Location" başlığıyla adresi gösterebildiği yerde doğrudur.
// Yanıt siparişin kimliğini taşır, istemci onu order ucundan okur.
//
// # İkinci çağrı
//
// Sepet tamamlanmış damgalandığı için ikinci çağrı errors.Conflict alır; akışın
// idempotency anahtarı da aynı sepette ikinci bir yürütmeyi engeller. Ağı kopan
// bir istemcinin isteği tekrarlaması bu yüzden ikinci sipariş üretmez.
func (h *Handler) storeCompleteCart(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body completeCartRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	if body.ExpectedTotal == nil {
		corehttp.WriteError(ctx, w, coreerrors.Invalid(codeInvalidRequest,
			"expected_total zorunludur; müşteriye onaylatılan toplam bildirilmelidir"))
		return
	}
	akis, err := h.checkout()
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	// Sepetin iletişim adresi KENDİ servisimizden okunur; istemciden alınmaz.
	id := cartID(r)
	detail, err := h.svc.GetCart(ctx, id)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	istek, err := json.Marshal(completeCartFlowRequest{
		CartID:            id,
		PaymentProviderID: body.PaymentProviderID,
		PaymentData:       body.PaymentData,
		Email:             detail.Email,
		ExpectedTotal:     *body.ExpectedTotal,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, coreerrors.Wrap(err, coreerrors.KindInternal, codeInvalidRequest,
			"sipariş tamamlama isteği kodlanamadı"))
		return
	}

	yanit, err := akis.CompleteCartJSON(ctx, istek)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	var sonuc completeCartFlowResult
	if err := json.Unmarshal(yanit, &sonuc); err != nil {
		corehttp.WriteError(ctx, w, coreerrors.Wrap(err, coreerrors.KindInternal, codeFlowResultInvalid,
			"sipariş tamamlama sonucu çözülemedi: %s", id))
		return
	}

	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: completeCartDTO{
		OrderID:      sonuc.OrderID,
		CartID:       sonuc.CartID,
		CurrencyCode: sonuc.CurrencyCode,
		Total:        sonuc.Amount,
	}})
}

// storeSetShippingAddress sepetin kargo adresini yazar.
func (h *Handler) storeSetShippingAddress(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body addressRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	addr, err := h.svc.SetShippingAddress(ctx, cartID(r), body.toInput())
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toAddressDTO(addr)})
}

// storeSetBillingAddress sepetin fatura adresini yazar.
func (h *Handler) storeSetBillingAddress(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body addressRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	addr, err := h.svc.SetBillingAddress(ctx, cartID(r), body.toInput())
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toAddressDTO(addr)})
}

// addShippingMethodRequest kargo yöntemi ekleme gövdesidir.
type addShippingMethodRequest struct {
	Name             string         `json:"name"`
	ShippingOptionID string         `json:"shipping_option_id"`
	Amount           int64          `json:"amount"`
	Data             map[string]any `json:"data"`
}

// storeAddShippingMethod sepete kargo yöntemi ekler.
func (h *Handler) storeAddShippingMethod(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body addShippingMethodRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	method, err := h.svc.AddShippingMethod(ctx, cartID(r), service.AddShippingMethodInput{
		Name:             body.Name,
		ShippingOptionID: body.ShippingOptionID,
		Amount:           body.Amount,
		Data:             body.Data,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusCreated, singleEnvelope{Data: toShippingMethodDTO(method)})
}

// storeRemoveShippingMethod kargo yöntemini sepetten kaldırır.
func (h *Handler) storeRemoveShippingMethod(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := h.svc.RemoveShippingMethod(ctx, cartID(r), chi.URLParam(r, paramMethodID)); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}
