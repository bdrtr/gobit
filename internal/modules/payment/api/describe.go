package api

import (
	"net/http"

	"github.com/bdrtr/gobit/internal/core/openapi"
)

// tutarNotu tutar taşıyan uçların açıklamasına eklenen birim uyarısıdır.
//
// Şema tipten "integer"/"int64" üretir ama TİP tek başına BİRİMİ söylemez:
// istemci geliştiricisi 100,50 TL'yi gönderemeyeceğini görüp yuvarlamayı ya da
// kuruşu düşürmeyi deneyebilir. Para hiçbir aşamada kayan noktaya uğramaz
// (plan Bölüm 8), bu yüzden doğru cevap 10050'dir ve bunu söyleyen tek şey bu
// nottur.
//
// Not ALAN düzeyinde değil İŞLEM düzeyinde durur: şema Go tipinden türetilir
// ve Go alanları açıklama taşımaz. Alan başına açıklama, çekirdeğe yeni bir
// mekanizma (etiketten okunan description) eklemek demekti ve bu, tek bir
// modülün içinden verilecek bir karar değildir.
const tutarNotu = "Tutarlar MINOR UNIT tam sayıdır (kuruş/cent): " +
	"100,50 TL için 10050 gönderilir; 100.50 gibi ondalıklı bir değer geçersizdir."

// Describe payment'ın uçlarını OpenAPI belgesine işler.
//
// # Neden bu pakette
//
// Anlatılan gövdeler bu paketin DIŞA KAPALI DTO'larıdır (createSessionRequest,
// sessionDTO …) ve şema onlardan yansımayla türetilir. Tipleri anlatabilmek
// için dışa açmak, yalnızca belge üretmek uğruna modülün yüzeyini genişletmek
// olurdu: dışa açık bir tip sözleşmedir ve dışarıdan kurulabilir hâle gelirdi.
// Modülün [openapi.Describer] uygulaması bu yüzden buraya delege eder.
//
// # Neden paket düzeyinde bir fonksiyon
//
// Anlatım hiçbir çalışma zamanı durumuna bakmaz — şema TİPLERDEN gelir. Metodu
// [Handler]'a bağlamak, belgenin servis kurulmuş olmasına bağlı OLDUĞUNU
// söylerdi; oysa Register hiç çalışmamışken de belge üretilebilir ve
// üretilmelidir.
//
// # İki yüzey de anlatılır
//
// Yönetim yüzeyi ödemenin tüm aşamalarını, mağaza yüzeyi müşterinin ödeme
// adımını taşır. Mağaza yüzeyinin DAR olması bilinçlidir (bkz. paket belgesi)
// ve şemada da öyle görünür: orada tahsilat ucu yoktur ve oturum açma gövdesi
// tutar taşımaz.
//
// # ANLATILMAYAN DÖRT UÇ: "Collection" bileşen adı ÇAKIŞIYOR
//
// Şema bileşeninin adı Go tip adından türetilir (baş harf büyür, "DTO" son eki
// atılır), yani [collectionDTO] "Collection", [createCollectionRequest] ise
// "CreateCollectionRequest" adını ister. Product modülünün belgesinde aynı iki
// ad zaten kayıtlıdır (ürün koleksiyonu). İki FARKLI tip aynı adı istediğinde
// [openapi.Doc.Build] hata döner ve belge TÜMDEN üretilemez: /openapi.json 500
// olur ve yalnızca bu modülün değil product'ın, cart'ın, hepsinin uçları
// belgeden düşer. Yani çakışmayı görmezden gelmek tek bir ucu değil belgenin
// tamamını kaybetmek demektir.
//
// Bu yüzden ödeme KOLEKSİYONU gövdesi taşıyan dört uç anlatılmadan BIRAKILDI:
//
//   - POST /admin/v1/payment-collections
//   - GET  /admin/v1/payment-collections
//   - GET  /admin/v1/payment-collections/{id}
//   - GET  /store/v1/payment-collections/{id}
//
// Dördü de belgede yolu, metodu ve güvenliğiyle görünür; yalnızca gövdeleri
// olmaz. Koleksiyonun ALTINDAKİ uçlar (oturumlar, tahsilatlar) etkilenmez;
// onların gövdesi oturum ve tahsilat kaydıdır, koleksiyon değil.
//
// Çözüm tiplerden birini YENİDEN ADLANDIRMAKTIR ve o karar bu modülün içinden
// verilemez: bileşen adı istemci üreteçlerinin ÜRETTİĞİ SINIF ADIDIR, yani
// yayımlanmış sözleşmedir ve bir kez istemci üretildikten sonra değiştirmek
// kırıcıdır. İki modülü birden ilgilendiren bir kararı tek modülün içinden
// vermek, öteki modülün istemcisini habersiz kırardı.
//
// # Sorgu parametresi YOKTUR
//
// Anlatılan uçların hiçbiri sorgu dizesini okumaz: oturum, tahsilat ve iade
// listeleri sayfalanmaz ([writeList] tüm kayıtları yazar) ve sağlayıcı listesi
// süzülmez. Sorgu dizesini okuyan TEK uç GET /admin/v1/payment-collections'tır
// ([parsePage] ile) ve o uç yukarıdaki çakışma yüzünden zaten anlatılmadı.
// Şemaya yine de bir parametre yazmak, istemciye ÇALIŞMAYAN bir özellik vaat
// etmek olurdu: istemci üreteci metoda bir argüman koyar, çağıran onu doldurur
// ve sunucu sessizce yok sayar.
//
// # Bilinen sınır: istek gövdelerinin "required" kümesi GENİŞTİR
//
// Çekirdek "required"ı encoding/json'un HER ZAMAN yazdığı alanlardan türetir
// ([openapi.Doc.SchemaOf]) ve bu, YANIT gövdeleri için doğru cevaptır. İstek
// gövdesinde ise "required" istemcinin GÖNDERMEK ZORUNDA olduğu alan demektir
// ve bunu tip bilemez: bu paketin istek DTO'ları omitempty taşımadığı için
// hepsi zorunlu görünür — örneğin oturum açarken opsiyonel olan "data" da
// istenir. Alan ADLARI ve TİPLERİ doğrudur, yani şema yanlış bir alan
// uydurmaz; yalnızca fazla şey ister.
func Describe(d *openapi.Doc) {
	// Sağlayıcı listesi İKİ yüzeyde de aynı handler'a bağlıdır ama ayrı ayrı
	// anlatılır: belge yol+metod başına yazılır ve tek bir kayıt ötekini
	// kapsamaz.
	d.Describe(http.MethodGet, pathAdminProviders, openapi.Operation{
		Summary: "Kurulu ödeme sağlayıcılarının kimliklerini listeler.",
		Responses: map[string]any{
			"200": openapi.Response("Sağlayıcı kimlikleri", d.List([]string{})),
		},
	})

	d.Describe(http.MethodGet, pathStoreProviders, openapi.Operation{
		Summary: "Vitrinde seçilebilecek ödeme sağlayıcılarının kimliklerini listeler.",
		Responses: map[string]any{
			"200": openapi.Response("Sağlayıcı kimlikleri", d.List([]string{})),
		},
	})

	describeOturumlar(d)
	describeTahsilatlar(d)
	describeMagaza(d)
}

// describeOturumlar yönetim yüzeyindeki ödeme oturumu uçlarını anlatır.
func describeOturumlar(d *openapi.Doc) {
	d.Describe(http.MethodGet, pathAdminCollectionSess, openapi.Operation{
		Summary:     "Koleksiyonun ödeme oturumlarını listeler.",
		Description: tutarNotu,
		Responses: map[string]any{
			// Liste SAYFALANMAZ; zarf yine de aynıdır (bkz. [writeList]):
			// count satır sayısıdır ve limit ile aynıdır.
			"200": openapi.Response("Koleksiyonun oturumları", d.List(sessionDTO{})),
		},
	})

	d.Describe(http.MethodPost, pathAdminCollectionSess, openapi.Operation{
		Summary: "Koleksiyon için bir sağlayıcıda ödeme oturumu açar.",
		Description: tutarNotu + " amount verilmezse koleksiyonun KALAN tutarının " +
			"tamamı için oturum açılır. idempotency_key zorunludur: aynı anahtarla " +
			"gelen ikinci istek yeni oturum AÇMAZ, var olanı döner.",
		RequestBody: d.RequestBody(createSessionRequest{}),
		Responses: map[string]any{
			// Handler 201 yazar (bkz. handlers.go); yeni bir oturum doğar.
			"201": openapi.Response("Açılan ödeme oturumu", d.Item(sessionDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathAdminSession, openapi.Operation{
		Summary:     "Ödeme oturumunu kimliğiyle döner.",
		Description: tutarNotu,
		Responses: map[string]any{
			"200": openapi.Response("Ödeme oturumu", d.Item(sessionDTO{})),
		},
	})

	d.Describe(http.MethodPost, pathAdminSessionAuthorize, openapi.Operation{
		Summary: "Ödeme oturumunu yetkilendirir ve tutarı bloke eder.",
		// Sağlayıcının REDDİ açıklamada anlatılır, ayrı bir "409" kaydı
		// olarak DEĞİL: hata gövdesi çekirdeğin ortak zarfıdır ve ona atıf
		// yapmanın yolu ("Error" bileşeninin adı) çekirdeğin iç ayrıntısıdır.
		// Burada tekrarlamak, ad değiştiği gün sessizce kırılan ikinci bir
		// kayıt yaratırdı. Ret bir sunucu hatası değildir; istenen geçişin
		// gerçekleşmemesidir ve gerekçe oturumun kendisinde döner.
		Description: tutarNotu + " Sağlayıcı reddederse istek 409 ile döner ve " +
			"gerekçe oturumun decline_reason alanında görünür.",
		Responses: map[string]any{
			// Yetkilendirme yeni bir kayıt üretmez, var olan oturumu ilerletir:
			// handler 200 yazar.
			"200": openapi.Response("Yetkilendirilmiş oturum", d.Item(sessionDTO{})),
		},
	})

	d.Describe(http.MethodPost, pathAdminSessionCapture, openapi.Operation{
		Summary: "Bloke edilmiş tutarı tahsil eder.",
		Description: tutarNotu + " Gövde hiç gönderilmeyebilir; amount verilmezse " +
			"ya da sıfırsa bloke tutarın TAMAMI tahsil edilir.",
		RequestBody: istegeBagliGovde(d, amountRequest{}),
		Responses: map[string]any{
			// Tahsilat AYRI bir kayıt doğurur (payment); handler 201 yazar.
			"201": openapi.Response("Üretilen tahsilat kaydı", d.Item(paymentDTO{})),
		},
	})

	d.Describe(http.MethodPost, pathAdminSessionCancel, openapi.Operation{
		Summary: "Ödeme oturumunu iptal eder ve bloke tutarı serbest bırakır.",
		Description: "İDEMPOTENTTİR: zaten iptal edilmiş bir oturum için de 204 " +
			"döner, hata değil.",
		Responses: map[string]any{
			"204": bosYanit("Oturum iptal edildi"),
		},
	})
}

// describeTahsilatlar tahsilat ve iade uçlarını anlatır.
func describeTahsilatlar(d *openapi.Doc) {
	d.Describe(http.MethodGet, pathAdminCollectionPays, openapi.Operation{
		Summary:     "Koleksiyonun tahsilatlarını listeler.",
		Description: tutarNotu,
		Responses: map[string]any{
			"200": openapi.Response("Koleksiyonun tahsilatları", d.List(paymentDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathAdminPayment, openapi.Operation{
		Summary:     "Tahsilatı kimliğiyle döner.",
		Description: tutarNotu,
		Responses: map[string]any{
			"200": openapi.Response("Tahsilat kaydı", d.Item(paymentDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathAdminPaymentRefund, openapi.Operation{
		Summary:     "Tahsilatın iadelerini listeler.",
		Description: tutarNotu,
		Responses: map[string]any{
			"200": openapi.Response("Tahsilatın iadeleri", d.List(refundDTO{})),
		},
	})

	d.Describe(http.MethodPost, pathAdminPaymentRefund, openapi.Operation{
		Summary: "Tahsilatı kısmen ya da tamamen iade eder.",
		Description: tutarNotu + " amount verilmezse ya da sıfırsa tahsilatın " +
			"KALAN tutarının tamamı iade edilir.",
		RequestBody: d.RequestBody(refundRequest{}),
		Responses: map[string]any{
			// İade AYRI bir kayıt doğurur; handler 201 yazar.
			"201": openapi.Response("Üretilen iade kaydı", d.Item(refundDTO{})),
		},
	})
}

// describeMagaza mağaza yüzeyinin ödeme uçlarını anlatır.
//
// Yüzeyin darlığı şemada da görünmelidir: burada tahsilat ucu YOKTUR ve oturum
// açma gövdesi ([createStoreSessionRequest]) tutar taşımaz. Şema admin
// gövdesini gösterseydi, istemci geliştiricisi sunucunun reddedeceği bir alanı
// gönderirdi (bkz. [decodeBody] — tanınmayan alanlar hata verir).
func describeMagaza(d *openapi.Doc) {
	d.Describe(http.MethodPost, pathStoreCollectSess, openapi.Operation{
		Summary: "Müşteri adına, koleksiyonun kalan tutarı için ödeme oturumu açar.",
		Description: tutarNotu + " Tutar İSTEMCİDEN alınmaz: oturum her zaman " +
			"koleksiyonun kalan tutarının tamamını kapar. Gövdede amount " +
			"gönderilirse istek reddedilir; data içindeki sağlayıcı davranış " +
			"anahtarları da kabul edilmez.",
		RequestBody: d.RequestBody(createStoreSessionRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("Açılan ödeme oturumu", d.Item(sessionDTO{})),
		},
	})

	d.Describe(http.MethodPost, pathStoreSessionCancel, openapi.Operation{
		Summary: "Müşterinin açtığı ödeme oturumunu bırakır.",
		Description: "Ödeme YÖNTEMİNİ değiştirmenin yoludur: açık bir oturum " +
			"koleksiyonun kalan tutarını kapatır. İDEMPOTENTTİR: zaten iptal " +
			"edilmiş bir oturum için de 204 döner.",
		Responses: map[string]any{
			"204": bosYanit("Oturum bırakıldı"),
		},
	})
}

// istegeBagliGovde İSTEĞE BAĞLI bir JSON istek gövdesi tanımı üretir.
//
// [openapi.Doc.RequestBody] gövdeyi HER ZAMAN zorunlu işaretler; tahsilat
// ucunda bu yanlış olurdu: gövde hiç gönderilmezse bloke tutarın tamamı
// tahsil edilir (bkz. [decodeOptionalAmount]) ve en yaygın çağrı budur.
// Zorunlu demek, istemci üretecinin çağıranı yalnızca şema öyle dediği için
// boş bir nesne kurmaya zorlaması demekti.
//
// Şema yine TİPTEN türetilir; elle yazılan tek şey zarfın "required" bayrağıdır.
func istegeBagliGovde(d *openapi.Doc, v any) map[string]any {
	return map[string]any{
		"required": false,
		"content": map[string]any{
			"application/json": map[string]any{"schema": d.SchemaOf(v)},
		},
	}
}

// bosYanit GÖVDESİZ bir yanıt tanımı üretir.
//
// [openapi.Response] her zaman bir gövde şeması yazar; 204'ün gövdesi ise
// YOKTUR (bkz. handlers.go, corehttp.WriteJSON'a nil verilen çağrılar). Boş
// bir şema yazmak "bir şey dönüyor ama şekli bilinmiyor" demek olurdu ve
// istemci üreteci okunacak bir gövde bekleyen bir metot üretirdi.
func bosYanit(aciklama string) map[string]any {
	return map[string]any{"description": aciklama}
}
