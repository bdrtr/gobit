package checkout

import (
	"context"
	"encoding/json"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// InteropName sipariş tamamlama akışının container'daki adıdır (ADR 0001/0006).
//
// Adı bu paket bildirir, kaydı BİLEŞİM KÖKÜ yapar (cmd/server): akış yedi
// modül yüzeyini container'dan çözer ve ancak tüm modüller Register olduktan
// SONRA kurulabilir. Tüketici cart MODÜLÜDÜR — sepeti siparişe çeviren HTTP
// ucunun sahibi sepetin sahibidir — ve adı kendi paketinde DİZE olarak
// tekrarlar (bkz. cart modülündeki CartCompletionName).
const InteropName = "workflows.checkout.interop"

// CodeInteropRequestInvalid çözülemeyen bir tamamlama isteği geldiğini
// bildirir.
const CodeInteropRequestInvalid = "checkout_interop_request_invalid"

// Interop sipariş tamamlama akışını modüller arası İLKEL yüzeye çevirir.
//
// # Neden JSON
//
// [CompleteCartInput] altı, [CompleteCartResult] on alan taşır ve ikisi de bu
// paketin tipidir; hiçbir modül onları adlandıramaz (ADR 0006). Geriye iki
// seçenek kalır: alanları konumsal parametrelere açmak ya da sınırı JSON'dan
// geçirmek. Birincisi altı parametreli bir imza üretirdi ve aynı tipteki iki
// dizeyi (sağlayıcı kimliği ile e-posta) yer değiştirmek derleme zamanında
// görünmezdi. JSON, iki tarafın da adlandırabildiği tek yapısal biçimdir ve
// şema TEK YERDE — [completeCartRequest] ve [completeCartResponse] — yazılıdır.
// Aynı örüntü promotion, tax ve fulfillment modüllerinin yüzeylerinde de
// kullanılır.
//
// # Yüzey akışın TAMAMI değildir
//
// Şema bilinçli olarak DARDIR: akışın kabul ettiği her alan burada yoktur ve
// ürettiği her alan buradan geçmez. Gerekçeler alanların yanında yazılıdır;
// ortak ölçüt şudur — bu yüzeyin arkasında publishable anahtarla erişilen bir
// mağaza ucu vardır ve oraya açılan her alan, müşterinin belirleyebildiği bir
// karar demektir.
type Interop struct {
	w *Workflows
}

// NewInterop verilen akış için modüller arası yüzeyi kurar.
func NewInterop(w *Workflows) *Interop { return &Interop{w: w} }

// completeCartRequest tamamlama isteğinin JSON şemasıdır.
//
// Alan adları tüketici tarafındaki şemayla BİREBİR aynı olmak zorundadır ve
// uyum ancak entegrasyon testiyle kanıtlanabilir: taraflar birbirini import
// edemediği için derleyici bu sınırı denetlemez.
type completeCartRequest struct {
	// CartID tamamlanacak sepettir; ZORUNLUDUR.
	CartID string `json:"cart_id"`
	// PaymentProviderID ödemenin açılacağı sağlayıcıdır; ZORUNLUDUR.
	//
	// Müşterinin SEÇİMİDİR ve bu yüzden yüzeyde vardır: hangi sağlayıcıdan
	// ödeneceğini sunucu müşteri adına seçemez. Yetki sorusu doğurmaz — ad
	// sunucuda KAYITLI bir sağlayıcıya çözülmek zorundadır; tanınmayan bir ad
	// tahsilat açmaz, akışı düşürür.
	PaymentProviderID string `json:"payment_provider_id"`
	// PaymentData sağlayıcıya olduğu gibi iletilen serbest JSON'dur;
	// opsiyoneldir (kart tokenı, dönüş adresi).
	PaymentData json.RawMessage `json:"payment_data,omitempty"`
	// Email siparişin iletişim adresidir; opsiyoneldir.
	Email string `json:"email,omitempty"`
	// ExpectedTotal çağıranın müşteriye ONAYLATTIĞI toplamdır (minor unit).
	//
	// Hesaplanan tutarla uyuşmazsa akış errors.Conflict döner ve HİÇBİR yan
	// etki uygulanmaz: karşılaştırma saga'nın ilk adımından önce, hesabın
	// yenilendiği hazırlıkta yapılır. Sıfır "karşılaştırma yapma" demektir
	// (bkz. [CompleteCartInput.ExpectedTotal]) ve bu, tutarı DÜŞÜREMEZ —
	// karşılaştırmayı atlayan çağıran yine sunucunun hesapladığı tutarı öder,
	// yalnızca fiyat değişmişse müşteriyi uyarma fırsatını kaybeder.
	ExpectedTotal int64 `json:"expected_total"`

	// LOKASYON BURADA YOKTUR ve olmayacaktır. [CompleteCartInput.LocationID]
	// hangi DEPODAN çıkılacağını sabitler; bu bir kargo kararıdır ve akış onu
	// satır başına stok + kargo modüllerine sorarak verir. Alanı bu yüzeye
	// açmak, mağaza istemcisine depo seçtirmek olurdu: hem stok topolojisini
	// dışarıya anlatır, hem de bir müşterinin siparişini istediği depodan
	// çıkartmasına izin verirdi. Belirli bir depodan çıkması gereken YÖNETİM
	// siparişi bu ucun konusu değildir ve geldiğinde kendi yüzeyini alır.
}

// completeCartResponse tamamlama sonucunun JSON şemasıdır.
//
// Şema akışın sonucundan DARDIR. Dışarıda kalanlar ve gerekçeleri:
//
//   - payment_collection_id, payment_session_id, payment_id, reservation_ids:
//     ödeme ve stok modüllerinin İÇ kimlikleridir. Siparişi takip etmek için
//     gerekli değildirler; yayımlanmaları, mağaza istemcisine hiçbir ucundan
//     kullanamayacağı iç yapıyı anlatmak olurdu.
//   - warnings: siparişi DÜŞÜRMEYEN ama elle onarım isteyen arızalardır ve
//     muhatabı operatördür, müşteri değil. Kaybolmazlar: akış onları zaten
//     ERROR seviyesinde loglar (bkz. clearCartStep.Invoke).
//   - cart_completed, reservations_confirmed: aynı arızaların bayrak hâlidir.
type completeCartResponse struct {
	// OrderID oluşan siparişin kimliğidir.
	OrderID string `json:"order_id"`
	// CartID siparişin doğduğu sepettir.
	CartID string `json:"cart_id"`
	// CurrencyCode tahsil edilen para birimidir (ISO 4217).
	CurrencyCode string `json:"currency_code"`
	// Amount tahsil edilen tutardır (minor unit).
	Amount int64 `json:"amount"`
}

// CompleteCartJSON sepeti siparişe çevirir.
//
// İstek [completeCartRequest], yanıt [completeCartResponse] şemasındadır.
// Akışın kendisi ve saga'nın telafi kuralları [Workflows.CompleteCart]
// godoc'undadır; bu metot yalnızca sınırı çevirir ve HİÇBİR karar vermez.
//
// Çözülemeyen bir istek gövdesi errors.Invalid'dir: çağıran bu paketi import
// edemez, dolayısıyla şemayı elle kurar ve bir yazım hatası ancak burada
// görülebilir. Sessizce sıfır değerlerle devam etmek, sepeti sağlayıcısız ve
// onaysız tamamlamaya çalışmak olurdu.
func (i *Interop) CompleteCartJSON(ctx context.Context, request json.RawMessage) (json.RawMessage, error) {
	if len(request) == 0 {
		return nil, errors.Invalid(CodeInteropRequestInvalid,
			"sipariş tamamlama isteği boş olamaz")
	}

	var istek completeCartRequest
	if err := json.Unmarshal(request, &istek); err != nil {
		return nil, errors.Wrap(err, errors.KindInvalid, CodeInteropRequestInvalid,
			"sipariş tamamlama isteği çözülemedi")
	}

	sonuc, err := i.w.CompleteCart(ctx, CompleteCartInput{
		CartID:            istek.CartID,
		PaymentProviderID: istek.PaymentProviderID,
		PaymentData:       istek.PaymentData,
		Email:             istek.Email,
		ExpectedTotal:     istek.ExpectedTotal,
	})
	if err != nil {
		return nil, err
	}

	return json.Marshal(completeCartResponse{
		OrderID:      sonuc.OrderID,
		CartID:       sonuc.CartID,
		CurrencyCode: sonuc.CurrencyCode,
		Amount:       sonuc.Amount,
	})
}
