package checkout

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/workflow"
)

// Workflow ve adım adları. Adlar yürütme kaydına yazılır ve elle müdahale
// gerektiren bir yürütmede operatörün gördüğü tek şeydir; değişmeleri geçmiş
// kayıtların okunmasını zorlaştırır.
const (
	// WorkflowName saga'nın motordaki adıdır.
	WorkflowName = "complete_cart"
	// StepReserveInventory stok ayırma adımının adıdır.
	StepReserveInventory = "reserve_inventory"
	// StepCreateOrder sipariş açma adımının adıdır.
	StepCreateOrder = "create_order"
	// StepAuthorizePayment ödeme yetkilendirme adımının adıdır.
	StepAuthorizePayment = "authorize_payment"
	// StepCapturePayment tahsilat adımının adıdır.
	StepCapturePayment = "capture_payment"
	// StepClearCart sepet kapatma adımının adıdır.
	StepClearCart = "clear_cart"
)

// IdempotencyKeyPrefix yürütme anahtarının sepet kimliğinden önce gelen
// bölümüdür.
//
// Anahtar (workflow adı, anahtar) çiftiyle tekil olduğu için önek teknik olarak
// gerekli değildir; okunabilirlik içindir: pgstore'daki bir satıra bakan
// operatör anahtarın neyi adlandırdığını başka bir tabloya bakmadan görür.
const IdempotencyKeyPrefix = "complete_cart:"

// CompensationTimeout tek bir telafi çağrısına verilen süre bütçesidir.
//
// Bütçe ADIM BAŞINADIR (bkz. [workflow.WithCompensationTimeout]) ve motorun
// varsayılanıyla aynı değerdedir; burada AÇIKÇA verilmesi, telafinin gerçek
// ağ çağrıları yaptığını (ödeme sağlayıcısına iptal, veritabanına rezervasyon
// bırakma) ve bu sürenin bir karar olduğunu görünür kılmak içindir.
const CompensationTimeout = 30 * time.Second

// SagaTimeout saga'nın TAMAMINA verilen süre bütçesidir.
//
// Bütçe cömerttir çünkü zincirde üç modül ve bir ödeme sağlayıcısı vardır;
// yine de SONLUDUR: çağıranın iptalinden ayrılmış bir akışın (bkz.
// [sagaContext]) süresiz koşma hakkı olamaz, aksi hâlde asılı bir dış çağrı
// bir goroutine'i ve ayrılmış stoğu sonsuza kadar tutardı.
const SagaTimeout = 2 * time.Minute

// CompleteCartInput sipariş tamamlama isteğinin girdisidir.
type CompleteCartInput struct {
	// CartID tamamlanacak sepettir; ZORUNLUDUR.
	CartID string
	// LocationID stoğun ayrılacağı stok lokasyonudur; ZORUNLUDUR.
	//
	// # TEK LOKASYON VARSAYIMI — Faz 7'de değişecektir
	//
	// Sepetin TÜM satırları aynı lokasyondan ayrılır ve lokasyonu bu akış
	// SEÇMEZ, çağıran bildirir. İki gerekçesi vardır: (1) hangi depodan
	// gönderileceği bir FULFILLMENT kararıdır ve kuralları (kargo bölgesi, satış
	// kanalı, stoğun dağılımı) plan Faz 7'de fulfillment modülüyle gelir;
	// (2) stok modülünün modüller arası yüzeyi lokasyon LİSTELEMEZ, yani bu
	// paketin "ilk lokasyonu" seçmesi teknik olarak da mümkün değildir ve
	// mümkün olsaydı bile sıralama tesadüfüne bağlı bir depo seçimi olurdu.
	//
	// Faz 7 geldiğinde bu alan opsiyonel hâle gelebilir ve boş bırakıldığında
	// lokasyonu fulfillment seçer; o gün geldiğinde satır BAŞINA farklı
	// lokasyon da gerekebilir ve [planLine] o yüzden kendi kimliklerini taşır.
	LocationID string
	// PaymentProviderID ödemenin açılacağı sağlayıcıdır; ZORUNLUDUR.
	//
	// Varsayılanı YOKTUR: hangi sağlayıcıdan tahsilat yapılacağı müşterinin
	// seçimidir ve bir varsayılan, yanlış kablolanmış bir kurulumda parayı
	// sessizce başka bir sağlayıcıdan çekerdi.
	PaymentProviderID string
	// PaymentData sağlayıcıya olduğu gibi iletilecek serbest JSON nesnesidir;
	// opsiyoneldir (kart tokenı, dönüş adresi, test davranış anahtarları).
	//
	// Yürütme kaydına YAZILMAZ; gerekçe için bkz. [checkoutPlan].
	PaymentData json.RawMessage
	// Email siparişin iletişim adresidir; opsiyoneldir.
	//
	// Sepetin kendi e-postası burada kullanılamaz: cart modülünün modüller
	// arası yüzeyi onu yayımlamaz. Misafir siparişinde e-posta tek takip
	// yoludur, bu yüzden ödeme adımında sorulup buraya verilmelidir.
	Email string
	// ExpectedTotal çağıranın müşteriye ONAYLATTIĞI toplam tutardır (minor
	// unit); opsiyoneldir ve sıfır "kontrol etme" demektir.
	//
	// Verilirse hesaplanan tutarla karşılaştırılır ve fark errors.Conflict
	// üretir. Kontrol gereklidir çünkü hesap checkout'un başında YENİLENİR:
	// katalogdaki bir fiyat değişikliği, müşterinin gördüğünden farklı bir
	// tutarın sessizce çekilmesine yol açabilirdi.
	ExpectedTotal int64
}

// CompleteCartResult tamamlanan siparişin çağıranı ilgilendiren alanlarıdır.
//
// Tip saga'nın SON adımının çıktısıdır ve yürütme kaydına JSON olarak yazılır;
// aynı anahtarla yapılan ikinci çağrı bu gövdeyi kayıttan okuyup döner. Alan
// adlarının değişmesi, eski kayıtların okunamaması demektir.
type CompleteCartResult struct {
	// CartID siparişin doğduğu sepettir.
	CartID string `json:"cart_id"`
	// OrderID oluşan siparişin kimliğidir.
	OrderID string `json:"order_id"`
	// PaymentCollectionID ödeme koleksiyonunun kimliğidir.
	PaymentCollectionID string `json:"payment_collection_id"`
	// PaymentSessionID ödeme oturumunun kimliğidir.
	PaymentSessionID string `json:"payment_session_id"`
	// PaymentID tahsilatın kimliğidir.
	PaymentID string `json:"payment_id"`
	// CurrencyCode tahsil edilen para birimidir.
	CurrencyCode string `json:"currency_code"`
	// Amount tahsil edilen tutardır (minor unit).
	Amount int64 `json:"amount"`
	// ReservationIDs siparişe ayrılan rezervasyonlardır.
	ReservationIDs []string `json:"reservation_ids"`
	// CartCompleted sepetin tamamlanmış damgalanıp damgalanmadığını bildirir.
	CartCompleted bool `json:"cart_completed"`
	// ReservationsConfirmed rezervasyonların kesinleştirilip
	// kesinleştirilmediğini bildirir.
	ReservationsConfirmed bool `json:"reservations_confirmed"`
	// Warnings siparişi DÜŞÜRMEYEN ama elle onarım isteyen arızalardır.
	//
	// Yalnızca pivot'tan sonraki adım (clear_cart) doldurur: para çekildikten
	// sonra sepetin damgalanamaması ya da rezervasyonun onaylanamaması, akışı
	// geri almak için bir gerekçe değildir (bkz. paket yorumu). Alan doluysa
	// sipariş GEÇERLİDİR, ama bir insan bakmalıdır.
	Warnings []string `json:"warnings,omitempty"`
}

// CompleteCart sepeti siparişe çevirir.
//
// Akış önce hazırlığı yapar (hesap yenilenir, anlık görüntü okunur, başlıklar
// ve stok kalemleri çözülür), sonra beş adımlı saga'yı çalıştırır:
// reserve_inventory -> create_order -> authorize_payment -> capture_payment ->
// clear_cart. Bir adım patlarsa o ana kadar başarılı adımlar TERS SIRADA
// telafi edilir; ayrıntılar ve pivot kuralı paket yorumundadır.
//
// Yürütme, sepet kimliğinden türetilen bir idempotency anahtarına bağlanır:
// aynı sepet için yapılan ikinci çağrı adımları TEKRAR ÇALIŞTIRMAZ. Sürüyorsa
// ya da telafi edilmişse errors.Conflict döner.
//
// # İkinci çağrı GERÇEK kurulumda hazırlıkta durur
//
// Motorun "tamamlanmış yürütmenin çıktısını dön" yolu (replay) bu akışta
// pratikte ERİŞİLEMEZDİR ve godoc bunu saklamaz: hazırlık motorun idempotency
// denetiminden ÖNCE çalışır ve ilk iş olarak hesabı yeniler, oysa başarılı bir
// yürütme sepeti tamamlanmış damgalar. Gerçek cart modülünde ikinci çağrının
// cevabı bu yüzden "aynı sonuç" değil, [CodeCartCompleted]'dır. Replay yolu
// yalnızca sepetin damgalanamadığı (clear_cart uyarısı bıraktığı) durumda
// görülebilir. Denetimi hazırlıktan önceye almak mümkündür ama motorun
// anahtarını hesap yenilenmeden bağlamak demektir ve o daha büyük bir tasarım
// kararıdır (plan Faz 7+).
//
// # Saga çağıranın İPTALİNDEN ayrılır
//
// Hazırlık çağıranın bağlamıyla koşar — hiçbir yan etki bırakmaz, dolayısıyla
// vazgeçen bir istemci için çalışmaya devam etmenin anlamı yoktur. Saga ise
// [sagaContext] ile ayrılır: ilk rezervasyon alındıktan sonra istemcinin
// düşmesi akışı DURDURMAZ. Sebep pivot'tadır — motor her adımdan önce bağlamı
// denetler ve tahsilat sırasında gelen bir iptal, clear_cart'ı tümüyle
// atlatırdı: para çekilmiş, sipariş açık, sepet kilitli ve stok "active"
// kalırdı. İdempotency anahtarı da yandığı için o sepet bir daha hiç
// denenemezdi. Yarım bırakılan iş, tamamlananın maliyetinden pahalıdır.
//
// Dönen hatanın sınıfı çağıranın dallanabilmesi için korunur: yetersiz stok ve
// değişmiş sepet errors.Conflict, fiyatsız/stoksuz varyant errors.Invalid,
// bulunamayan sepet errors.NotFound'dur. Telafinin kendisi patlarsa hata
// errors.Internal'dır ve ELLE MÜDAHALE gerektirir.
//
// # Adımlar YENİDEN DENENMEZ
//
// Motorun varsayılanı korunur (bkz. [workflow.NoRetry]): inventory.Reserve iki
// kez çağrılırsa iki rezervasyon üretir, payment.Capture'ın tekrarı ise gerçek
// bir sağlayıcıda ikinci bir para hareketi denemesidir. Buna karşılık TELAFİ
// yeniden denenir; başarısız bir telafinin bedeli elle müdahaledir ve geçici
// bir arızada ısrar etmek karşılığını verir.
func (w *Workflows) CompleteCart(ctx context.Context, in CompleteCartInput) (CompleteCartResult, error) {
	if err := in.normalize(); err != nil {
		return CompleteCartResult{}, err
	}

	plan, err := w.prepare(ctx, in)
	if err != nil {
		return CompleteCartResult{}, err
	}

	wf := workflow.Workflow{
		Name: WorkflowName,
		Steps: []workflow.Step{
			&reserveInventoryStep{w: w, plan: plan},
			&createOrderStep{w: w, plan: plan},
			&authorizePaymentStep{w: w, plan: plan},
			&capturePaymentStep{w: w, plan: plan},
			&clearCartStep{w: w, plan: plan},
		},
	}

	sctx, cancel := sagaContext(ctx)
	defer cancel()

	out, err := workflow.RunInto[CompleteCartResult](sctx, w.executor, wf, plan,
		workflow.WithIdempotencyKey(IdempotencyKeyPrefix+plan.CartID),
		workflow.WithCompensationRetry(compensationRetry()),
		workflow.WithCompensationTimeout(CompensationTimeout),
	)
	if err != nil {
		return CompleteCartResult{}, err
	}

	w.log.InfoContext(ctx, "sepet siparişe çevrildi",
		"cart_id", out.CartID, "order_id", out.OrderID, "payment_id", out.PaymentID,
		"amount", out.Amount, "currency_code", out.CurrencyCode,
		"warnings", len(out.Warnings))
	return out, nil
}

// sagaContext saga'yı çağıranın İPTALİNDEN ayırır ve kendi süre bütçesine
// bağlar.
//
// Motor her adımdan ÖNCE bağlamı denetler ve ölü bir bağlamda yeni adım
// başlatmaz; bu, yan etki bırakmayan bir akışta doğru davranıştır ama burada
// PIVOT vardır. Tahsilat akışın en yavaş dış çağrısıdır ve tam o sırada gelen
// bir iptal (istemci düştü, ağ geçidi zaman aşımına uğradı) son adımı —
// sepetin damgalanması ve rezervasyonların kesinleştirilmesi — tümüyle
// atlatırdı. Sonuç, paketin "asla olmamalı" dediği duruma yakın bir yerdir:
// para çekilmiş, sipariş "pending", sepet kilitli, stok "active" ve yürütme
// compensation_failed olduğu için aynı sepet bir daha denenemez.
//
// Bağlamın DEĞERLERİ korunur (context.WithoutCancel): izleme kimlikleri ve
// logger alanları akışın geri kalanında görünür kalmalıdır. Bütçe
// [SagaTimeout]'tur; çağıranın bütçesi değil, çünkü ayrılmanın amacı tam da
// onun bitmesine dayanmaktır.
func sagaContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), SagaTimeout)
}

// compensationRetry telafi zincirinin yeniden deneme politikasıdır.
//
// Üstel geri çekilme kısa tutulmuştur: telafi, adım başına
// [CompensationTimeout] bütçesini denemelerle PAYLAŞIR ve uzun beklemeler o
// bütçeyi işe değil beklemeye harcardı. Kalıcı hatalar (errors.Conflict,
// errors.Invalid) motor tarafından zaten denenmez, yani politika yalnızca
// geçici arızalarda çalışır.
func compensationRetry() workflow.RetryPolicy {
	return workflow.RetryPolicy{
		MaxAttempts: 3,
		Backoff:     200 * time.Millisecond,
		Multiplier:  2,
		MaxBackoff:  2 * time.Second,
	}
}

// normalize girdiyi doğrular ve boşlukları kırpar.
//
// Kırpma yalnızca e-postada yapılır; kimlikler kırpılmaz, reddedilir
// (bkz. [requireID]).
func (in *CompleteCartInput) normalize() error {
	if err := requireID("cart_id", in.CartID, MaxCartIDLen); err != nil {
		return err
	}
	if err := requireID("location_id", in.LocationID, maxIDLen); err != nil {
		return err
	}
	if err := requireID("payment_provider_id", in.PaymentProviderID, maxIDLen); err != nil {
		return err
	}
	if in.ExpectedTotal < 0 {
		return errors.Invalid(CodeInvalidInput,
			"expected_total negatif olamaz: %d", in.ExpectedTotal)
	}
	if in.ExpectedTotal > MaxTotal {
		return errors.Invalid(CodeInvalidInput,
			"expected_total en fazla %d olabilir: %d", MaxTotal, in.ExpectedTotal)
	}

	in.Email = strings.TrimSpace(in.Email)
	if len(in.Email) > maxIDLen {
		return errors.Invalid(CodeInvalidInput,
			"email en fazla %d bayt olabilir: %d", maxIDLen, len(in.Email))
	}
	return nil
}
