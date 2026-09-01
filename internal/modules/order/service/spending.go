package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// SpendingPolicy siparişi VEREN müşteriye uygulanacak harcama kuralını
// bildiren yüzeydir.
//
// # Arayüz neden BURADA tanımlı
//
// ADR 0001'in örüntüsü: tüketici kendi dar arayüzünü kendi paketinde tanımlar,
// sağlayıcının somut tipi onu YAPISAL olarak karşılar ve container'dan ADLA
// çözülür. Kuralın kaynağı b2b modülüdür ama bu paket onu import EDEMEZ; bu
// yüzden imza yalnızca ilkel ve stdlib tipleri kullanır ve bileşik veri JSON
// olarak geçer.
//
// # Kuralı neden bu modül uyguluyor
//
// Kural iki bilgiyi birleştirir: LİMİT (b2b'nin verisi) ve HARCAMA (bu modülün
// verisi — verilmiş siparişlerin toplamı). Birleştirmeyi çağıran tarafta
// (örneğin complete_cart saga'sında) yapmak mümkündü ama yanlış olurdu: kontrol
// ile siparişin yazılması iki ayrı işleme düşer ve iki eşzamanlı sipariş
// limitin altında görünüp birlikte limiti aşabilirdi. Burada kontrol,
// siparişin yazıldığı İŞLEMİN İÇİNDE ve müşteri kilidi altında yapılır
// (bkz. [Service.enforceSpendingLimit]).
//
// İkinci sebep KAÇIŞTIR: bu modülde sipariş yaratan tek yol
// [Service.CreateOrder]'dır (yönetim yüzeyinde sipariş açan bir uç YOKTUR).
// Kural oraya konduğunda, siparişi hangi akışın açtığından bağımsız olarak
// uygulanır; saga'ya konsaydı, ileride eklenecek ikinci bir çağıran kuralı
// sessizce atlardı.
//
// # OPSİYONELDİR
//
// Bağımlılık nil bırakılabilir ve bırakıldığında davranış b2b modülü hiç
// yokmuş gibidir: hiçbir okuma, hiçbir kilit, hiçbir ek karar. Saf B2C bir
// kurulumun harcama limiti diye bir kavramı yoktur ve o kurulumda her siparişe
// bir sorgu daha yüklemek bedelsiz değildir.
//
// # Kural KİME uygulanır
//
// Yalnızca [CreateOrderInput.CustomerID] dolu olan siparişlere. O alan bugün
// vitrinin BEYANIDIR ve hiçbir katman onu doğrulamaz; kuralın hangi koşulda
// uygulanMADIĞI [Service.spendingRuleFor] godoc'undaki güven sınırı bölümünde
// ve ADR 0008'de yazılıdır. Yüzeyi kuran gömülü uygulamanın bilmesi gereken
// şey budur: bu arayüzü bağlamak, limitin uygulandığını KENDİ BAŞINA
// garanti etmez.
type SpendingPolicy interface {
	// SpendingLimitJSON müşteriye uygulanacak kuralı döner.
	//
	// Gövde [spendingRule] şemasındadır. Müşterinin kuralı yoksa (B2B çalışanı
	// değil ya da limiti sınırsız) çağrı BAŞARILIDIR ve "limited": false döner;
	// hata dönmez. Hata dönmesi, kuralın OKUNAMADIĞI anlamına gelir ve sipariş
	// reddedilir.
	SpendingLimitJSON(ctx context.Context, customerID string) (json.RawMessage, error)
}

// spendingRule bir müşteriye uygulanacak harcama kuralının JSON şemasıdır.
//
// Alan adları sağlayıcı tarafındaki şemayla BİREBİR aynı olmak ZORUNDADIR; bu
// paket sağlayıcıyı import edemediği için derleyici uyumu denetleyemez ve uyum
// ancak entegrasyon testiyle kanıtlanabilir (ADR 0001'in kabul edilen bedeli).
//
//	{
//	  "limited":        true,
//	  "spending_limit": 500000,                 // minor unit TAM SAYI
//	  "currency_code":  "TRY",                  // LİMİTİN para birimi
//	  "window_start":   "2026-09-01T00:00:00Z"  // BOŞ ise pencere yoktur
//	}
type spendingRule struct {
	Limited       bool   `json:"limited"`
	SpendingLimit int64  `json:"spending_limit"`
	CurrencyCode  string `json:"currency_code"`
	WindowStart   string `json:"window_start"`

	// windowStart çözülmüş pencere başlangıcıdır; JSON'dan gelmez.
	windowStart *time.Time
}

// spendingRuleFor müşteriye uygulanacak kuralı okur ve DOĞRULAR.
//
// Okuma işlemin DIŞINDA yapılır ve bu bilinçlidir: sağlayıcı başka bir modüldür
// ve kendi bağlantısını kullanır. Açık bir sipariş işlemi tutarken başka bir
// modülün sorgusunu beklemek, havuzdaki bağlantıyı dış bir çağrının süresi
// boyunca kilitlemek olurdu.
//
// Bunun bedeli, limitin okunmasıyla siparişin yazılması arasında limitin
// DEĞİŞEBİLMESİDİR: tam o anda limiti düşüren bir yönetici, bu siparişte hâlâ
// eski limiti görür. Kabul edilebilir, çünkü korunması gereken şey limitin en
// güncel değeri değil, TOPLAMIN tutarlılığıdır; toplam kilit altında okunur.
//
// Politika kurulu değilse (nil) ya da müşteri yoksa kural "sınırsız"dır.
//
// # GÜVEN SINIRI: kural, çağıranın BEYAN ETTİĞİ müşteriye uygulanır
//
// Bu fonksiyonun aldığı customerID bir OLGU değil bir İDDİADIR ve bu modül onu
// doğrulayamaz. Kimliğin kaynağı vitrin sepetinin gövdesindeki "customer_id"
// alanıdır; mağaza yüzeyinin tek kimliği publishable API anahtarıdır ve o bir
// SATIŞ KANALINI temsil eder, bir müşteriyi değil (bkz. corehttp.Principal —
// alanları arasında müşteri kimliği YOKTUR). Yani sunucunun "isteği gerçekten
// bu müşteri yaptı" diyebileceği bir kanıt hiçbir katmanda üretilmiyor.
//
// Sonucu tek cümleyle: harcama limiti, MÜŞTERİYİ BEYAN EDEN alışverişlere
// uygulanır. Beyan etmeyen alışverişe uygulanmaz ve bu iki durum ölçüldü —
// aynı sepet, aynı anahtar, tek fark gövdedeki alan:
//
//	{"country_code":"TR","customer_id":"cus_…"}  -> 409 order_spending_limit_exceeded
//	{"country_code":"TR"}                        -> 200, sipariş açılır
//
// Kaçış üç biçimde ifade edilebilir ve üçü de bu satırın ALTINDAN geçer:
// alanı hiç göndermemek (misafir), başkasının kimliğini göndermek (harcama
// onun penceresinden düşer — limitli bir çalışanın hakkını yakmanın da yolu
// budur) ve POST /store/v1/customers ile yepyeni bir misafir kaydı açıp onu
// göndermek (yeni kayıt hiçbir şirkete bağlı olmadığı için kuralsızdır).
//
// # Kapatma NEDEN buraya konmadı
//
// Kapatılabilecek bir şey yok: kaçış "yanlış bir iddia" değil, "hiç iddia
// etmemek"tir. Beyanı ZORUNLU kılmak da işe yaramaz — üçüncü biçim bir istek
// daha atarak yeni bir kimlik üretir. İddiayı KANITA bağlamak için bir müşteri
// oturumu gerekir ve o, bu modülün değil çerçevenin kararıdır; sorumluluğun
// nerede durduğu ADR 0008'de yazılıdır.
//
// Bu yüzden buradaki dal bir kusur değil, sınırın GÖRÜNDÜĞÜ yerdir ve
// TestMisafirSiparisindeHarcamaKuraliHicSorulmaz ile sabitlenmiştir: kimliği
// doğrulayan bir katman eklendiğinde değişmesi gereken ilk yer burasıdır.
func (s *Service) spendingRuleFor(ctx context.Context, customerID string) (spendingRule, error) {
	// Misafir siparişinde uygulanacak bir kural yoktur: kural çalışana
	// bağlıdır ve çalışanın kimliği bir müşteri kaydıdır. Kimliğin
	// DOĞRULANMADIĞI bu godoc'un güven sınırı bölümündedir.
	if s.spending == nil || customerID == "" {
		return spendingRule{}, nil
	}

	payload, err := s.spending.SpendingLimitJSON(ctx, customerID)
	if err != nil {
		// Sınıf KORUNUR: geçici bir arıza (Unavailable) ile bozuk bir kurulum
		// (Invalid) çağıran için farklı dallardır. Hiçbir durumda sipariş
		// GEÇMEZ — kuralı okuyamadan yazmak, limiti sessizce kaldırmak olurdu.
		return spendingRule{}, errors.Wrap(err, errors.KindOf(err), CodeSpendingPolicyUnavailable,
			"harcama kuralı okunamadı: %s", customerID)
	}

	var rule spendingRule
	if len(payload) == 0 {
		return spendingRule{}, errors.Internal(CodeSpendingPolicyInvalid,
			"harcama kuralı boş geldi: %s", customerID)
	}
	if err := json.Unmarshal(payload, &rule); err != nil {
		return spendingRule{}, errors.Wrap(err, errors.KindInternal, CodeSpendingPolicyInvalid,
			"harcama kuralı çözülemedi: %s", customerID)
	}
	if !rule.Limited {
		return spendingRule{}, nil
	}

	if rule.SpendingLimit < 0 {
		return spendingRule{}, errors.Internal(CodeSpendingPolicyInvalid,
			"harcama limiti negatif geldi: %s -> %d", customerID, rule.SpendingLimit)
	}
	// Kod SAKLANMADAN önce tekleştirilir: siparişin para birimi zaten BÜYÜK
	// harfe indirilmiştir (bkz. normalizeCreateOrder) ve iki taraf farklı
	// yazımda kalırsa karşılaştırma "TRY" ile "try"ı ayrı para birimi sanar —
	// yani sağlayıcının küçük harf göndermesi limiti sessizce uygulanamaz
	// kılardı.
	currency, err := normalizeCurrency(rule.CurrencyCode)
	if err != nil {
		return spendingRule{}, errors.Wrap(err, errors.KindInternal, CodeSpendingPolicyInvalid,
			"harcama limitinin para birimi okunamadı: %s -> %q", customerID, rule.CurrencyCode)
	}
	rule.CurrencyCode = currency
	if rule.WindowStart != "" {
		start, err := time.Parse(time.RFC3339, rule.WindowStart)
		if err != nil {
			return spendingRule{}, errors.Wrap(err, errors.KindInternal, CodeSpendingPolicyInvalid,
				"harcama penceresinin başlangıcı çözülemedi: %s -> %q", customerID, rule.WindowStart)
		}
		utc := start.UTC()
		rule.windowStart = &utc
	}
	return rule, nil
}

// checkCurrency siparişin para biriminin limitle ÖLÇÜLEBİLİR olduğunu
// doğrular.
//
// # Neden çevirmiyoruz
//
// Şirketin limiti bir para biriminde ifade edilir; sipariş başka bir para
// biriminde verilirse ikisi TOPLANAMAZ. Çevirmek için bir kur gerekir ve bu
// depoda kur diye bir veri YOKTUR — uydurulmuş bir kur, yanlış olduğu hiç
// görünmeyen bir sayı üretir ve harcama limiti tam da yanlış sayıya
// dayanmaması gereken bir karardır.
//
// # Neden ATLAMIYORUZ da REDDEDİYORUZ
//
// İkinci seçenek, farklı para birimli siparişi kuralın dışında bırakmaktı.
// O durumda kural, kaçmak isteyen için bir kapıya dönüşürdü: limiti dolmuş bir
// çalışan başka para birimli bir bölgeden alışverişe devam edebilirdi.
// Uygulanamayan bir kural sessizce atlanmaz; uygulanamadığı SÖYLENİR.
//
// Bedeli açıktır ve kabul edilmiştir: para birimi şirketinkinden farklı bir
// bölgede, harcama limitli bir çalışan sipariş VEREMEZ. Doğru çözüm o şirkete
// o para biriminde ikinci bir kayıt (ve limit) tanımlamaktır; sessizce limitsiz
// alışveriş değil.
func (r spendingRule) checkCurrency(orderCurrency string) error {
	if !r.Limited || r.CurrencyCode == orderCurrency {
		return nil
	}
	return errors.Conflict(CodeSpendingCurrencyMismatch,
		"harcama limiti %s cinsinden tanımlı; %s cinsinden sipariş bu limite karşı ölçülemez",
		r.CurrencyCode, orderCurrency)
}

// enforceSpendingLimit müşterinin harcamasını okur ve limiti UYGULAR.
//
// # Yalnızca işlem içinde çağrılır
//
// Çağrı [Service.writeOrder]'ın işleminin İLK işidir. Sıra şudur: müşteri
// kilidi -> toplamı oku -> karşılaştır -> siparişi yaz. Kilit işlem sonuna
// kadar tutulduğu için aynı müşteri için gelen ikinci bir sipariş bekler ve
// toplamı BİRİNCİNİN yazdığı satırla birlikte okur. Kilitsiz hâlde iki
// eşzamanlı istek de limitin altında görünür ve ikisi de yazılırdı — kontrol
// ile yazma arasındaki yarış tam olarak budur.
//
// # Karşılaştırma
//
// Kural: pencere içindeki harcama + bu siparişin tutarı > limit ise sipariş
// REDDEDİLİR (limite eşit olan geçer; limit harcanabilecek TAVANDIR).
//
// Toplama yerine çıkarma kullanılır: harcama, veritabanındaki bir SUM'dan
// gelir ve tek bir sipariş tutarının sınırlarına tabi değildir, yani
// "harcama + tutar" taşabilir. "harcama > limit - tutar" ise iki terimi de
// sınırlı olan bir çıkarmadır ve taşamaz.
func (s *Service) enforceSpendingLimit(ctx context.Context, rule spendingRule, in CreateOrderInput) error {
	if !rule.Limited {
		return nil
	}

	if err := s.store.LockCustomerSpending(ctx, in.CustomerID); err != nil {
		return err
	}
	spent, err := s.store.SumCustomerSpend(ctx, in.CustomerID, in.CurrencyCode, rule.windowStart)
	if err != nil {
		return err
	}
	if spent > rule.SpendingLimit-in.Total {
		return errors.Conflict(CodeSpendingLimitExceeded,
			"harcama limiti aşılıyor: dönem içi harcama %d, sipariş %d, limit %d (%s)",
			spent, in.Total, rule.SpendingLimit, in.CurrencyCode)
	}

	s.log.InfoContext(ctx, "harcama limiti uygulandı",
		"customer_id", in.CustomerID, "spent", spent, "amount", in.Total,
		"limit", rule.SpendingLimit, "currency_code", in.CurrencyCode)
	return nil
}
