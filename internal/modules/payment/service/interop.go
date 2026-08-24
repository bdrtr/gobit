package service

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// Bu dosya payment modülünün MODÜLLER ARASI yüzeyidir (ADR 0001, ADR 0006).
//
// Faz 6'nın complete_cart saga'sı (internal/workflows) bu modülü import
// EDEMEZ. Çözüm region/cart modüllerindeki interop.go ile aynıdır: yalnızca
// İLKEL ve stdlib tipleri kullanan bir yüzey yayımlamak. Tüketici kendi dar
// arayüzünü tanımlar, bu tip onu YAPISAL olarak karşılar ve container'dan
// "payment.interop" adıyla çözülür.
//
// Sebep Go'nun yapısal uyum kuralıdır: tüketici payment'ı import edemediği için
// imzasında models.PaymentSession gibi bir tipi adlandıramaz; adlandırdığı an
// o, kendi paketinde tanımlı BAŞKA bir tip olur ve somut servis tüketicinin
// arayüzünü karşılamaz.
//
// Yüzey BİLİNÇLİ OLARAK dardır ve saga'nın ihtiyacına göre seçilmiştir:
// koleksiyon aç, oturum aç, yetkilendir, tahsil et, iptal et (telafi), iade et
// ve durumu oku. Buraya eklenen her metot, payment'ı ayrı bir servise
// çıkarmanın maliyetini artırır.
//
// # Yüzey TUTAR taşır
//
// Okuma metotları yalnızca durum dizesi değil, TUTAR da döner
// ([Interop.Collection], [Interop.Authorize]). Saga'nın ödemenin tam olduğunu
// kendi doğrulaması şarttır: durum dizesi türetilmiş bir özettir ve eksik bir
// ödemeyi tam gösterecek biçimde değişebilir. Sayı dönmek, doğrulamayı bu
// modülün durum türetiminden bağımsız kılar.

// Interop payment servisini modüller arası İLKEL yüzeye çevirir.
//
// Hiçbir karar vermez: yalnızca imzayı çevirir. Tüm iş kuralları [Service]
// üzerinde kalır; buraya kural eklemek, aynı kuralın iki yerde ayrışması demek
// olurdu.
type Interop struct {
	svc *Service
}

// NewInterop verilen servis için modüller arası yüzeyi kurar.
func NewInterop(svc *Service) *Interop { return &Interop{svc: svc} }

// CreateCollection bir referans için ödeme koleksiyonu açar ve kimliğini döner.
//
// reference sepetin ya da siparişin kimliğidir; bu modül onu doğrulamaz
// (Prensip 2.2 — bağ Module Links ile kurulur).
func (i *Interop) CreateCollection(
	ctx context.Context,
	reference, currencyCode string,
	amount int64,
) (string, error) {
	col, err := i.svc.CreatePaymentCollection(ctx, CreateCollectionInput{
		Reference:    reference,
		CurrencyCode: currencyCode,
		Amount:       amount,
	})
	if err != nil {
		return "", err
	}
	return col.ID, nil
}

// OpenSession koleksiyon için bir sağlayıcıda ödeme oturumu açar ve oturumun
// kimliğini döner.
//
// Tutar verilmez: koleksiyonun HENÜZ BLOKE EDİLMEMİŞ tutarının tamamı için
// oturum açılır. Saga'nın ihtiyacı budur ve kısmi ödeme akışları (birden çok
// oturumla bölünmüş tahsilat) bu yüzeyin değil, admin API'sinin konusudur.
//
// Aynı idempotencyKey ile ikinci çağrı YENİ oturum açmaz, mevcut oturumun
// kimliğini döner; saga bir adımı yeniden denediğinde müşteriden ikinci kez
// tahsilat denenmemesini sağlayan şey budur.
func (i *Interop) OpenSession(
	ctx context.Context,
	collectionID, providerID, idempotencyKey string,
) (string, error) {
	return i.OpenSessionWithData(ctx, collectionID, providerID, idempotencyKey, nil)
}

// OpenSessionWithData oturumu, sağlayıcıya iletilecek serbest veriyle açar.
//
// data ham JSON nesnesidir (örn. kart tokenı) ve sağlayıcıya olduğu gibi
// geçirilir. Boş ya da JSON null verilirse veri yok sayılır.
//
// Sayılar json.Number olarak çözülür; harita üzerinden geçen bir tam sayının
// float64'e dönüp yeniden kodlanırken üstel gösterime kayması ("1e+15") ya da
// büyük değerlerde duyarlık kaybetmesi böyle engellenir. Sağlayıcıya iletilen
// veride TUTAR bulunabilir (bkz. manual paketindeki davranış anahtarları) ve
// para hiçbir aşamada kayan noktaya uğramamalıdır (plan Bölüm 8).
func (i *Interop) OpenSessionWithData(
	ctx context.Context,
	collectionID, providerID, idempotencyKey string,
	data json.RawMessage,
) (string, error) {
	decoded, err := decodeInteropData(data)
	if err != nil {
		return "", err
	}

	ses, err := i.svc.CreateSession(ctx, collectionID, providerID, CreateSessionInput{
		IdempotencyKey: idempotencyKey,
		Data:           decoded,
	})
	if err != nil {
		return "", err
	}
	return ses.ID, nil
}

// Authorize oturumu yetkilendirir; oturumun YENİ durumunu ve fiilen BLOKE
// EDİLEN tutarı döner.
//
// Sağlayıcı reddederse hata döner (errors.Conflict, kod
// [CodeAuthorizationDeclined]) ve oturum "failed" olarak kalıcı yazılır. Saga
// adımı bu hatayla patlar ve telafi zinciri devreye girer; ret sessizce
// yutulsaydı ödenmemiş bir sipariş onaylanırdı.
//
// Bloke tutarın dönmesi zorunludur: sağlayıcı KISMİ yetkilendirebilir ve o
// hâlde durum yine "authorized" olur. Yalnızca duruma bakan bir saga, istenenin
// altında bloke edilmiş bir ödemeyi tam sanardı.
func (i *Interop) Authorize(ctx context.Context, sessionID string) (status string, authorized int64, err error) {
	ses, err := i.svc.AuthorizePayment(ctx, sessionID)
	if err != nil {
		return "", 0, err
	}
	return ses.Status.String(), ses.AuthorizedAmount, nil
}

// Capture bloke edilmiş tutarı tahsil eder ve oluşan tahsilatın kimliğini
// döner. amount sıfırsa bloke tutarın tamamı çekilir.
func (i *Interop) Capture(ctx context.Context, sessionID string, amount int64) (string, error) {
	payment, err := i.svc.CapturePayment(ctx, sessionID, amount)
	if err != nil {
		return "", err
	}
	return payment.ID, nil
}

// Cancel oturumu iptal eder; SAGA TELAFİSİ budur ve İDEMPOTENTTİR.
//
// İki kez çağrılırsa ikinci çağrı hata VERMEZ. Bilinmeyen bir oturum kimliği
// ise errors.NotFound döner; telafi, var olmayan bir kaydı sessizce yutmaz.
func (i *Interop) Cancel(ctx context.Context, sessionID string) error {
	return i.svc.CancelPayment(ctx, sessionID)
}

// Refund tahsilatı iade eder ve oluşan iade kaydının kimliğini döner.
// amount sıfırsa kalan tutarın tamamı iade edilir.
func (i *Interop) Refund(ctx context.Context, paymentID string, amount int64, reason string) (string, error) {
	refund, err := i.svc.RefundPayment(ctx, paymentID, amount, reason)
	if err != nil {
		return "", err
	}
	return refund.ID, nil
}

// Collection koleksiyonun güncel durumunu ve TUTARLARINI döner.
//
// Saga ödemenin TAM olduğunu kendi doğrulamak zorundadır ve tek penceresi bu
// yüzeydir; durum dizesi tek başına yetmez. Durum, tutarlardan türetilen bir
// ÖZETTİR (bkz. [models.CollectionStatusFor]) ve her yeni ayrım için yeni bir
// değer eklemek, tüketicinin dizeleri ezberlemesi demek olurdu. Tutarlar
// döndüğünde saga'nın kuralı tek satırdır: captured >= amount.
//
// Dönen değerler sırasıyla koleksiyonun durumu, toplanması gereken tutar,
// bloke edilen, tahsil edilen ve iade edilen tutardır (hepsi minor unit).
//
// İmza uzundur ve bu bilinçlidir: tüketici bu paketi import EDEMEDİĞİ için
// ortak bir yapı tipi adlandıramaz (ADR 0006) ve tutarlar ancak ayrı ilkel
// değerler olarak taşınabilir. Hepsinin TEK okumadan dönmesi ayrıca saga'nın,
// iki çağrı arasında değişen bir koleksiyonu tutarsız görmesini engeller.
//
//nolint:gocritic // Sonuç sayısı ADR 0006'nın ilkel-tip kısıtından gelir; gerekçe yukarıda.
func (i *Interop) Collection(ctx context.Context, collectionID string) (
	status string,
	amount, authorized, captured, refunded int64,
	err error,
) {
	col, err := i.svc.GetPaymentCollection(ctx, collectionID)
	if err != nil {
		return "", 0, 0, 0, 0, err
	}
	return col.Status.String(), col.Amount, col.AuthorizedAmount, col.CapturedAmount, col.RefundedAmount, nil
}

// SessionStatus oturumun güncel durumunu döner.
//
// Telafinin gerçekten çalıştığını doğrulayan testler buna bakar: iptal edilmiş
// bir oturum "canceled" döner ve saga'nın geri alma zinciri gözle görülür olur.
func (i *Interop) SessionStatus(ctx context.Context, sessionID string) (string, error) {
	ses, err := i.svc.GetPaymentSession(ctx, sessionID)
	if err != nil {
		return "", err
	}
	return ses.Status.String(), nil
}

// decodeInteropData ham JSON gövdesini sağlayıcıya verilecek haritaya çevirir.
//
// Sayılar json.Number olarak bırakılır; gerekçe için bkz.
// [Interop.OpenSessionWithData].
func decodeInteropData(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	var out map[string]any
	if err := dec.Decode(&out); err != nil {
		return nil, errors.Wrap(err, errors.KindInvalid, CodeInvalidInput,
			"oturum verisi çözümlenemedi; JSON nesnesi olmalı")
	}
	return out, nil
}
