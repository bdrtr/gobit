package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// Bu dosya b2b modülünün MODÜLLER ARASI yüzeyidir (ADR 0001).
//
// Harcama limitini UYGULAYAN taraf order modülüdür: harcamanın kendisi
// (verilmiş siparişlerin toplamı) onun verisidir ve kuralın yazma anıyla aynı
// işlemde uygulanabildiği tek yer orasıdır. Ama limitin NE OLDUĞU bu modülün
// verisidir. İkisi birbirini import edemez, bu yüzden bağ diğer modüllerdeki
// interop.go dosyalarıyla aynı biçimde kurulur: yalnızca İLKEL ve stdlib
// tipleri kullanan bir yüzey yayımlanır, tüketici kendi dar arayüzünü kendi
// paketinde tanımlar ve somut tip container'dan "b2b.interop" adıyla çözülür.
//
// Tüketici tarafındaki karşılığı şudur (order kendi paketinde tanımlar):
//
//	type SpendingPolicy interface {
//	    SpendingLimitJSON(ctx context.Context, customerID string) (json.RawMessage, error)
//	}
//
// Bileşik veri JSON olarak taşınır. Alan adları aşağıda AÇIKÇA beyan edilir;
// tüketici tarafındaki şema ile birebir aynı olmak ZORUNDADIR ve uyum ancak
// entegrasyon testiyle kanıtlanabilir — bu modül order paketini import
// edemediği için derleyici uyumu denetleyemez.

// Interop b2b servisini modüller arası İLKEL yüzeye çevirir.
//
// Hiçbir karar vermez: yalnızca imzayı ve JSON şemasını çevirir. "Limit aşıldı
// mı" sorusunu BU taraf yanıtlamaz; yanıtlayabilmesi için pencere içindeki
// sipariş toplamını bilmesi gerekirdi ve o veri order modülünündür.
//
// Container'a "b2b.interop" adıyla kaydedilir.
type Interop struct {
	svc *Service
}

// NewInterop verilen servis için modüller arası yüzeyi kurar.
func NewInterop(svc *Service) *Interop { return &Interop{svc: svc} }

// interopSpendingRule bir müşteriye uygulanacak harcama kuralının JSON
// şemasıdır.
//
// # Şema
//
//	{
//	  "limited":        true,                   // false ise diğer alanlar ANLAMSIZDIR
//	  "spending_limit": 500000,                 // minor unit TAM SAYI
//	  "currency_code":  "TRY",                  // ŞİRKETİN para birimi
//	  "window_start":   "2026-09-01T00:00:00Z"  // BOŞ ise pencere yoktur
//	}
//
// # Neden "kalan hak" değil de "limit + pencere"
//
// Kalanı hesaplamak, pencere içinde verilmiş siparişlerin toplamını gerektirir;
// o veri order modülünündür. Buradan kalan dönmek, b2b'nin order'ı okuması
// (yani tam da link katmanının kaldırdığı bağımlılık) demek olurdu. Bunun
// yerine yüzey KURALI taşır ve kuralı olguya uygulayan taraf, olgunun sahibi
// olan modüldür.
//
// # window_start neyi söyler
//
// Pencerenin başlangıcı ŞİRKETİN sıfırlama periyodundan türer ve TAKVİME
// göredir (bkz. models.SpendingResetPeriod): aylık limit her ayın 1'inde,
// yıllık limit 1 Ocak'ta sıfırlanır ve pencere UTC'dir. Alan BOŞ dize ise
// pencere yoktur ([models.ResetNever]) ve limit çalışanın TÜM geçmişine
// uygulanır — sıfır bir zaman damgası göndermek yerine boş dize seçilmesinin
// sebebi budur: "1 Ocak 0001'den beri" ile "pencere yok" farklı cümlelerdir ve
// ikincisi bir tarih değildir.
//
// Zaman RFC 3339 dizesidir, tam sayı değil: tüketici onu tek satırda çözer ve
// bir kodlama kararına (saniye mi milisaniye mi) bağlı kalmaz.
//
// # BİLİNEN SINIR: dönem ortasında şirket değiştiren çalışan
//
// Pencere takvimden gelir, çalışanın İŞE BAŞLAMA anından değil. Bir müşteri
// dönem ortasında A şirketinden çıkıp B şirketine çalışan olarak eklenirse,
// A'da yaptığı harcama B'nin penceresinde de sayılır — çünkü tüketici
// harcamayı MÜŞTERİ kimliğiyle toplar ve o kimlik değişmemiştir.
//
// Sapma tek yönlüdür ve KISITLAYICIDIR: çalışan hak ettiğinden AZ harcayabilir,
// asla fazlasını değil. Bu yüzden bilinçli olarak düzeltilmedi — düzeltmesi
// pencereyi çalışan kaydının doğum anıyla sınırlamaktır ve o, "dönem"in
// tanımını takvimden ilişkiye kaydıran ayrı bir karardır (muhasebe dönemiyle
// örtüşmesi gerekir). Bugün ödenen bedel, yılda birkaç kayıtta görülen
// fazladan bir kısıttır; sessizce alınmış bir karar değildir.
type interopSpendingRule struct {
	Limited       bool   `json:"limited"`
	SpendingLimit int64  `json:"spending_limit"`
	CurrencyCode  string `json:"currency_code"`
	WindowStart   string `json:"window_start"`
}

// SpendingLimitJSON müşteriye uygulanacak harcama kuralını döner.
//
// Şema [interopSpendingRule] belgesinde tanımlıdır.
//
// # Kuralı OLMAYAN müşteri HATA DEĞİLDİR
//
// Çağrı üç durumda da "limited": false döner ve BAŞARILIDIR:
//
//   - Müşteri hiçbir şirketin çalışanı değil (B2C alışverişi; kurulumun
//     çoğunluğu budur).
//   - Müşteri çalışan ama harcama limiti nil, yani SINIRSIZ.
//   - Verilen kimlik bir müşteri kimliği bile değil (önek tutmuyor). Böyle bir
//     kimlik çalışan olarak BAĞLANAMAZ (bkz. [Service.CreateEmployee]), yani
//     "bu müşterinin limiti yok" cevabı tahmin değil, kanıtlanabilir bir
//     olgudur.
//
// Üçünde de hata dönmek yanlış olurdu: tüketici bu yüzeyi HER sipariş için
// çağırır ve "bu müşteri B2B değil" cevabı onun için normal yoldur. Hata
// dönmek, tüketiciyi "kural yok" ile "kuralı öğrenemedik" arasında ayrım
// yapamaz hâle getirirdi — birincisi siparişi geçirmeli, ikincisi
// DURDURMALIDIR.
//
// Bunun dışındaki her hata (veritabanı arızası, bağ katmanının okunamaması)
// OLDUĞU GİBİ döner ve tüketici siparişi reddeder. Kuralın okunamadığı bir anda
// siparişi geçirmek, limiti sessizce kaldırmak olurdu.
func (i *Interop) SpendingLimitJSON(ctx context.Context, customerID string) (json.RawMessage, error) {
	uyelik, err := i.svc.MembershipOfCustomer(ctx, customerID)
	switch {
	case err == nil:
		// Kural aşağıda kurulur.
	case errors.IsNotFound(err), errors.IsInvalid(err):
		return json.Marshal(interopSpendingRule{})
	default:
		return nil, err
	}

	if !uyelik.Employee.HasSpendingLimit() {
		return json.Marshal(interopSpendingRule{})
	}

	kural := interopSpendingRule{
		Limited:       true,
		SpendingLimit: *uyelik.Employee.SpendingLimit,
		CurrencyCode:  uyelik.Company.CurrencyCode,
	}
	if uyelik.SpendingWindowStart != nil {
		kural.WindowStart = uyelik.SpendingWindowStart.UTC().Format(time.RFC3339)
	}
	return json.Marshal(kural)
}
